package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("monza-agent: starting")

	mountFS()

	ln, err := listenVsock(agentPort)
	if err != nil {
		log.Fatalf("monza-agent: vsock listen: %v", err)
	}
	defer ln.Close()
	log.Printf("monza-agent: listening on vsock port %d", agentPort)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("monza-agent: accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

const agentPort = 1024

// mountFS is defined in mount_linux.go / mount_other.go

func handleConnection(conn net.Conn) {
	defer conn.Close()
	log.Println("monza-agent: new connection")

	enc := json.NewEncoder(conn)
	var mu sync.Mutex
	send := func(resp response) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(resp)
	}

	send(response{Type: msgReady})

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024) // 4 MiB max message

	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			send(response{Type: msgError, Message: "invalid json: " + err.Error()})
			continue
		}

		switch req.Type {
		case msgExec:
			go handleExec(req, send)
		case msgWriteFile:
			go handleWriteFile(req, send)
		case msgReadFile:
			go handleReadFile(req, send)
		default:
			send(response{Type: msgError, ID: req.ID, Message: "unknown message type: " + req.Type})
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("monza-agent: scanner error: %v", err)
	}
	log.Println("monza-agent: connection closed")
}

func handleExec(req request, send func(response)) {
	cmd := exec.Command("/bin/sh", "-lc", req.Command)
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			send(response{Type: msgError, ID: req.ID, Message: fmt.Sprintf("exec error: %v", err)})
			return
		}
	}

	send(response{Type: msgStdout, ID: req.ID, Data: string(out)})
	send(response{Type: msgExit, ID: req.ID, Code: exitCode})
}
