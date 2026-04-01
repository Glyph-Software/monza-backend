package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"monza/backend/internal/microvm"
)

// Client communicates with the monza-agent running inside a microVM over a
// net.Conn (typically vsock). It multiplexes concurrent requests using
// request IDs and routes responses to the correct caller.
type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	mu      sync.Mutex // serialises writes
	pending sync.Map   // id -> chan microvm.Response
	nextID  atomic.Uint64
	done    chan struct{}
}

// New wraps an existing connection to the guest agent. The caller should have
// already dialled the vsock and received the "ready" message.
func New(conn net.Conn) *Client {
	c := &Client{
		conn: conn,
		enc:  json.NewEncoder(conn),
		done: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Dial connects to the guest agent, waits for the "ready" message, and
// returns a ready-to-use client. It retries until ctx is cancelled.
func Dial(ctx context.Context, dial func() (net.Conn, error)) (*Client, error) {
	var conn net.Conn
	var err error

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		conn, err = dial()
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent dial: context cancelled after error: %w", err)
		case <-ticker.C:
		}
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	readyCh := make(chan error, 1)
	go func() {
		if scanner.Scan() {
			var resp microvm.Response
			if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
				readyCh <- fmt.Errorf("agent ready: invalid json: %w", err)
				return
			}
			if resp.Type != microvm.MsgTypeReady {
				readyCh <- fmt.Errorf("agent ready: unexpected type %q", resp.Type)
				return
			}
			readyCh <- nil
		} else {
			readyCh <- fmt.Errorf("agent ready: connection closed: %v", scanner.Err())
		}
	}()

	select {
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	case err := <-readyCh:
		if err != nil {
			conn.Close()
			return nil, err
		}
	}

	return New(conn), nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		var resp microvm.Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}
		if resp.ID == "" {
			continue
		}
		if ch, ok := c.pending.Load(resp.ID); ok {
			ch.(chan microvm.Response) <- resp
		}
	}
}

func (c *Client) send(req microvm.Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(req)
}

func (c *Client) allocID() string {
	return fmt.Sprintf("r%d", c.nextID.Add(1))
}

func (c *Client) registerPending(id string) chan microvm.Response {
	ch := make(chan microvm.Response, 16)
	c.pending.Store(id, ch)
	return ch
}

func (c *Client) unregisterPending(id string) {
	c.pending.Delete(id)
}

// Exec runs a shell command in the guest and returns combined output.
func (c *Client) Exec(ctx context.Context, command string, maxBytes int) (microvm.ExecResult, error) {
	id := c.allocID()
	ch := c.registerPending(id)
	defer c.unregisterPending(id)

	req := microvm.Request{
		Type:    microvm.MsgTypeExec,
		ID:      id,
		Command: command,
	}
	if err := c.send(req); err != nil {
		return microvm.ExecResult{}, fmt.Errorf("exec send: %w", err)
	}

	var output bytes.Buffer
	truncated := false

	for {
		select {
		case <-ctx.Done():
			return microvm.ExecResult{}, ctx.Err()
		case <-c.done:
			return microvm.ExecResult{}, fmt.Errorf("agent connection closed")
		case resp := <-ch:
			switch resp.Type {
			case microvm.MsgTypeStdout, microvm.MsgTypeStderr:
				if maxBytes > 0 && output.Len()+len(resp.Data) > maxBytes {
					remaining := maxBytes - output.Len()
					if remaining > 0 {
						output.WriteString(resp.Data[:remaining])
					}
					truncated = true
				} else {
					output.WriteString(resp.Data)
				}
			case microvm.MsgTypeExit:
				return microvm.ExecResult{
					Output:    output.String(),
					ExitCode:  resp.Code,
					Truncated: truncated,
				}, nil
			case microvm.MsgTypeError:
				return microvm.ExecResult{}, fmt.Errorf("agent exec error: %s", resp.Message)
			}
		}
	}
}

// WriteFile sends file content to the guest agent.
func (c *Client) WriteFile(ctx context.Context, guestPath string, content io.Reader, mode os.FileMode) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read content: %w", err)
	}

	id := c.allocID()
	ch := c.registerPending(id)
	defer c.unregisterPending(id)

	req := microvm.Request{
		Type: microvm.MsgTypeWriteFile,
		ID:   id,
		Path: guestPath,
		Data: base64.StdEncoding.EncodeToString(data),
		Mode: uint32(mode),
	}
	if err := c.send(req); err != nil {
		return fmt.Errorf("write_file send: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("agent connection closed")
	case resp := <-ch:
		if resp.Type == microvm.MsgTypeError {
			return fmt.Errorf("agent write error: %s", resp.Message)
		}
		return nil
	}
}

// ReadFile retrieves a file from the guest. Returns an io.ReadCloser of the
// raw bytes and the file size.
func (c *Client) ReadFile(ctx context.Context, guestPath string) (io.ReadCloser, int64, error) {
	id := c.allocID()
	ch := c.registerPending(id)
	defer c.unregisterPending(id)

	req := microvm.Request{
		Type: microvm.MsgTypeReadFile,
		ID:   id,
		Path: guestPath,
	}
	if err := c.send(req); err != nil {
		return nil, 0, fmt.Errorf("read_file send: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-c.done:
		return nil, 0, fmt.Errorf("agent connection closed")
	case resp := <-ch:
		if resp.Type == microvm.MsgTypeError {
			return nil, 0, fmt.Errorf("agent read error: %s", resp.Message)
		}
		data, err := base64.StdEncoding.DecodeString(resp.Data)
		if err != nil {
			return nil, 0, fmt.Errorf("base64 decode: %w", err)
		}
		return io.NopCloser(bytes.NewReader(data)), resp.Size, nil
	}
}

// Close shuts down the client connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
