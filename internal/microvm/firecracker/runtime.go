//go:build linux

package firecracker

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	sdk "github.com/firecracker-microvm/firecracker-go-sdk"
	sdkmodels "github.com/firecracker-microvm/firecracker-go-sdk/client/models"

	"monza/backend/internal/microvm"
	"monza/backend/internal/microvm/agentclient"
	"monza/backend/internal/microvm/image"
	"monza/backend/internal/microvm/kernel"
)

// vmInstance tracks a running Firecracker VM and its agent connection.
type vmInstance struct {
	machine   *sdk.Machine
	agent     *agentclient.Client
	rootfs    string // path to the per-instance writable rootfs
	socketDir string // directory holding the API socket
	tapName   string // TAP device name (empty if no networking)
}

// Config holds settings for the Firecracker runtime.
type Config struct {
	FirecrackerBin string // path to firecracker binary; default: search PATH
	KernelCacheDir string
	ImageCacheDir  string
	AgentBinPath   string // cross-compiled monza-agent for linux/amd64 or linux/arm64
	InitScriptPath string
	NetworkSubnet  string // e.g. "172.16.0.0/16"; empty = no networking
	RootfsSizeMB   int
}

// Runtime implements microvm.Runtime using Firecracker on Linux.
type Runtime struct {
	cfg       Config
	imageMgr  *image.Manager
	kernelMgr *kernel.Manager

	mu        sync.Mutex
	instances map[string]*vmInstance // handle -> instance
	nextCID   uint32                // next vsock CID to assign

	ipAlloc *ipAllocator
}

// New creates a Firecracker runtime. The firecracker binary must be available
// in PATH or specified in cfg.FirecrackerBin.
func New(cfg Config) (*Runtime, error) {
	if cfg.FirecrackerBin == "" {
		p, err := findFirecracker()
		if err != nil {
			return nil, err
		}
		cfg.FirecrackerBin = p
	}

	imgMgr, err := image.NewManager(image.ManagerConfig{
		CacheDir:       cfg.ImageCacheDir,
		AgentBinPath:   cfg.AgentBinPath,
		InitScriptPath: cfg.InitScriptPath,
		RootfsSizeMB:   cfg.RootfsSizeMB,
	})
	if err != nil {
		return nil, fmt.Errorf("image manager: %w", err)
	}

	kernelMgr, err := kernel.NewManager(cfg.KernelCacheDir)
	if err != nil {
		return nil, fmt.Errorf("kernel manager: %w", err)
	}

	var alloc *ipAllocator
	if cfg.NetworkSubnet != "" {
		alloc, err = newIPAllocator(cfg.NetworkSubnet)
		if err != nil {
			return nil, fmt.Errorf("ip allocator: %w", err)
		}
	}

	return &Runtime{
		cfg:       cfg,
		imageMgr:  imgMgr,
		kernelMgr: kernelMgr,
		instances: make(map[string]*vmInstance),
		nextCID:   3, // CID 0=hypervisor, 1=loopback, 2=host
		ipAlloc:   alloc,
	}, nil
}

func (r *Runtime) Provision(ctx context.Context, opts microvm.ProvisionOpts) (string, error) {
	handle := opts.Name
	if handle == "" {
		return "", fmt.Errorf("sandbox name is required")
	}

	log.Printf("firecracker: provisioning %s (image=%s, mem=%dMiB, vcpus=%d)",
		handle, opts.Image, opts.MemoryMiB, opts.VCPUs)

	kernelPath, err := r.kernelMgr.EnsureKernel("")
	if err != nil {
		return "", fmt.Errorf("ensure kernel: %w", err)
	}

	baseRootfs, err := r.imageMgr.Prepare(ctx, opts.Image)
	if err != nil {
		return "", fmt.Errorf("prepare image: %w", err)
	}

	instanceRootfs, err := r.imageMgr.CopyRootfs(baseRootfs, handle)
	if err != nil {
		return "", fmt.Errorf("copy rootfs: %w", err)
	}

	socketDir, err := os.MkdirTemp("", "monza-fc-"+handle+"-*")
	if err != nil {
		return "", err
	}
	socketPath := filepath.Join(socketDir, "api.sock")

	r.mu.Lock()
	cid := r.nextCID
	r.nextCID++
	r.mu.Unlock()

	mem := int64(opts.MemoryMiB)
	if mem <= 0 {
		mem = 512
	}
	vcpus := int64(opts.VCPUs)
	if vcpus <= 0 {
		vcpus = 1
	}

	kernelArgs := "console=ttyS0 reboot=k panic=1 pci=off init=/init"
	for k, v := range opts.EnvVars {
		kernelArgs += fmt.Sprintf(" monza.env.%s=%s", k, v)
	}
	kernelArgs += fmt.Sprintf(" monza.hostname=%s", handle)

	fcCfg := sdk.Config{
		SocketPath:      socketPath,
		KernelImagePath: kernelPath,
		KernelArgs:      kernelArgs,
		MachineCfg: sdkmodels.MachineConfiguration{
			VcpuCount:  sdk.Int64(vcpus),
			MemSizeMib: sdk.Int64(mem),
		},
		Drives: []sdkmodels.Drive{
			{
				DriveID:      sdk.String("rootfs"),
				PathOnHost:   sdk.String(instanceRootfs),
				IsRootDevice: sdk.Bool(true),
				IsReadOnly:   sdk.Bool(false),
			},
		},
		VsockDevices: []sdk.VsockDevice{
			{Path: "vsock", CID: cid},
		},
	}

	var tapName string
	if r.ipAlloc != nil {
		hostIP, guestIP, err := r.ipAlloc.Allocate(handle)
		if err != nil {
			return "", fmt.Errorf("allocate IP: %w", err)
		}
		tapName = "tap-" + handle
		if len(tapName) > 15 {
			tapName = tapName[:15]
		}

		if err := setupTAP(tapName, hostIP); err != nil {
			r.ipAlloc.Release(handle)
			return "", fmt.Errorf("setup TAP: %w", err)
		}

		iface := sdk.NetworkInterface{
			StaticConfiguration: &sdk.StaticNetworkConfiguration{
				MacAddress:  generateMAC(cid),
				HostDevName: tapName,
				IPConfiguration: &sdk.IPConfiguration{
					IPAddr: net.IPNet{
						IP:   guestIP,
						Mask: net.CIDRMask(30, 32),
					},
					Gateway:     hostIP,
					Nameservers: []string{"8.8.8.8", "8.8.4.4"},
				},
			},
		}
		fcCfg.NetworkInterfaces = []sdk.NetworkInterface{iface}
	}

	machine, err := sdk.NewMachine(ctx, fcCfg,
		sdk.WithProcessRunner(sdk.VMCommandBuilder{}.
			WithBin(r.cfg.FirecrackerBin).
			WithSocketPath(socketPath).
			Build(ctx)),
	)
	if err != nil {
		r.cleanup(handle, instanceRootfs, socketDir, tapName)
		return "", fmt.Errorf("new machine: %w", err)
	}

	if err := machine.Start(ctx); err != nil {
		r.cleanup(handle, instanceRootfs, socketDir, tapName)
		return "", fmt.Errorf("start machine: %w", err)
	}

	vsockPath := filepath.Join(socketDir, fmt.Sprintf("vsock-%d", cid))
	agentClient, err := agentclient.Dial(ctx, func() (net.Conn, error) {
		return net.Dial("unix", vsockPath)
	})
	if err != nil {
		machine.StopVMM()
		r.cleanup(handle, instanceRootfs, socketDir, tapName)
		return "", fmt.Errorf("dial agent: %w", err)
	}

	inst := &vmInstance{
		machine:   machine,
		agent:     agentClient,
		rootfs:    instanceRootfs,
		socketDir: socketDir,
		tapName:   tapName,
	}

	r.mu.Lock()
	r.instances[handle] = inst
	r.mu.Unlock()

	log.Printf("firecracker: provisioned %s (cid=%d)", handle, cid)
	return handle, nil
}

func (r *Runtime) Destroy(ctx context.Context, handle string) error {
	r.mu.Lock()
	inst, ok := r.instances[handle]
	if ok {
		delete(r.instances, handle)
	}
	r.mu.Unlock()

	if !ok {
		return nil
	}

	log.Printf("firecracker: destroying %s", handle)

	if inst.agent != nil {
		inst.agent.Close()
	}
	if inst.machine != nil {
		inst.machine.StopVMM()
	}
	r.cleanup(handle, inst.rootfs, inst.socketDir, inst.tapName)
	return nil
}

func (r *Runtime) Exec(ctx context.Context, handle string, command string, maxBytes int) (microvm.ExecResult, error) {
	inst, err := r.getInstance(handle)
	if err != nil {
		return microvm.ExecResult{}, err
	}
	return inst.agent.Exec(ctx, command, maxBytes)
}

func (r *Runtime) WriteFile(ctx context.Context, handle string, guestPath string, content io.Reader, mode os.FileMode) error {
	inst, err := r.getInstance(handle)
	if err != nil {
		return err
	}
	return inst.agent.WriteFile(ctx, guestPath, content, mode)
}

func (r *Runtime) ReadFile(ctx context.Context, handle string, guestPath string) (io.ReadCloser, int64, error) {
	inst, err := r.getInstance(handle)
	if err != nil {
		return nil, 0, err
	}
	return inst.agent.ReadFile(ctx, guestPath)
}

func (r *Runtime) Close() error {
	r.mu.Lock()
	handles := make([]string, 0, len(r.instances))
	for h := range r.instances {
		handles = append(handles, h)
	}
	r.mu.Unlock()

	for _, h := range handles {
		r.Destroy(context.Background(), h)
	}
	return nil
}

func (r *Runtime) getInstance(handle string) (*vmInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.instances[handle]
	if !ok {
		return nil, fmt.Errorf("VM %q not found", handle)
	}
	return inst, nil
}

func (r *Runtime) cleanup(handle, rootfs, socketDir, tapName string) {
	if tapName != "" {
		teardownTAP(tapName)
		if r.ipAlloc != nil {
			r.ipAlloc.Release(handle)
		}
	}
	r.imageMgr.CleanupInstance(handle)
	if socketDir != "" {
		os.RemoveAll(socketDir)
	}
}

func findFirecracker() (string, error) {
	path, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(path)
		candidate := filepath.Join(dir, "firecracker")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	candidates := []string{
		"/usr/local/bin/firecracker",
		"/usr/bin/firecracker",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", fmt.Errorf("firecracker binary not found in PATH or standard locations")
}
