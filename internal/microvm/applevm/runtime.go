//go:build darwin

package applevm

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"sync"

	"github.com/Code-Hex/vz/v3"

	"monza/backend/internal/microvm"
	"monza/backend/internal/microvm/agentclient"
	"monza/backend/internal/microvm/image"
	"monza/backend/internal/microvm/kernel"
)

// vmInstance tracks a running Apple VM and its agent connection.
type vmInstance struct {
	vm        *vz.VirtualMachine
	agent     *agentclient.Client
	rootfs    string
	vsockDev  *vz.VirtioSocketDevice
}

// Config holds settings for the Apple VM runtime.
type Config struct {
	KernelCacheDir string
	ImageCacheDir  string
	AgentBinPath   string
	InitScriptPath string
	RootfsSizeMB   int
}

// Runtime implements microvm.Runtime using Apple Virtualization.framework.
type Runtime struct {
	cfg       Config
	imageMgr  *image.Manager
	kernelMgr *kernel.Manager

	mu        sync.Mutex
	instances map[string]*vmInstance

	// vmOps serialises VM lifecycle operations to the main OS thread as
	// required by Virtualization.framework.
	vmOps chan func()
}

// New creates an Apple VM runtime. Requires macOS 13+ on Apple Silicon and
// the com.apple.security.virtualization entitlement.
func New(cfg Config) (*Runtime, error) {
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

	r := &Runtime{
		cfg:       cfg,
		imageMgr:  imgMgr,
		kernelMgr: kernelMgr,
		instances: make(map[string]*vmInstance),
		vmOps:     make(chan func(), 64),
	}

	go r.vmThread()
	return r, nil
}

// vmThread pins itself to an OS thread and executes all VM lifecycle
// operations serially, as required by Virtualization.framework.
func (r *Runtime) vmThread() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for fn := range r.vmOps {
		fn()
	}
}

// runOnVMThread dispatches a closure to the VM thread and blocks until done.
func (r *Runtime) runOnVMThread(fn func() error) error {
	errCh := make(chan error, 1)
	r.vmOps <- func() {
		errCh <- fn()
	}
	return <-errCh
}

func (r *Runtime) Provision(ctx context.Context, opts microvm.ProvisionOpts) (string, error) {
	handle := opts.Name
	if handle == "" {
		return "", fmt.Errorf("sandbox name is required")
	}

	log.Printf("applevm: provisioning %s (image=%s, mem=%dMiB, vcpus=%d)",
		handle, opts.Image, opts.MemoryMiB, opts.VCPUs)

	kernelPath, err := r.kernelMgr.EnsureKernel("arm64")
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

	mem := uint64(opts.MemoryMiB)
	if mem == 0 {
		mem = 512
	}
	vcpus := uint(opts.VCPUs)
	if vcpus == 0 {
		vcpus = 1
	}

	kernelArgs := "console=hvc0 reboot=k panic=1 init=/init"
	for k, v := range opts.EnvVars {
		kernelArgs += fmt.Sprintf(" monza.env.%s=%s", k, v)
	}
	kernelArgs += fmt.Sprintf(" monza.hostname=%s", handle)

	var vm *vz.VirtualMachine
	var vsockDev *vz.VirtioSocketDevice

	err = r.runOnVMThread(func() error {
		bootLoader, err := vz.NewLinuxBootLoader(kernelPath, vz.WithCommandLine(kernelArgs))
		if err != nil {
			return fmt.Errorf("boot loader: %w", err)
		}

		vmConfig, err := vz.NewVirtualMachineConfiguration(bootLoader, vcpus, mem*1024*1024)
		if err != nil {
			return fmt.Errorf("vm config: %w", err)
		}

		diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithCacheAndSync(
			instanceRootfs, false, vz.DiskImageCachingModeAutomatic, vz.DiskImageSynchronizationModeFsync,
		)
		if err != nil {
			return fmt.Errorf("disk attachment: %w", err)
		}

		blockDev, err := vz.NewVirtioBlockDeviceConfiguration(diskAttachment)
		if err != nil {
			return fmt.Errorf("block device: %w", err)
		}
		vmConfig.SetStorageDevicesVirtualMachineConfiguration([]vz.StorageDeviceConfiguration{blockDev})

		vsockConfig, err := vz.NewVirtioSocketDeviceConfiguration()
		if err != nil {
			return fmt.Errorf("vsock config: %w", err)
		}
		vmConfig.SetSocketDevicesVirtualMachineConfiguration([]vz.SocketDeviceConfiguration{vsockConfig})

		natAttachment, err := vz.NewNATNetworkDeviceAttachment()
		if err != nil {
			return fmt.Errorf("NAT attachment: %w", err)
		}
		netDev, err := vz.NewVirtioNetworkDeviceConfiguration(natAttachment)
		if err != nil {
			return fmt.Errorf("network device: %w", err)
		}
		vmConfig.SetNetworkDevicesVirtualMachineConfiguration([]*vz.VirtioNetworkDeviceConfiguration{netDev})

		entropyDev, err := vz.NewVirtioEntropyDeviceConfiguration()
		if err != nil {
			return fmt.Errorf("entropy: %w", err)
		}
		vmConfig.SetEntropyDevicesVirtualMachineConfiguration([]*vz.VirtioEntropyDeviceConfiguration{entropyDev})

		serialAttachment, err := vz.NewFileHandleSerialPortAttachment(os.Stdin, os.Stdout)
		if err != nil {
			return fmt.Errorf("serial attachment: %w", err)
		}
		serialPort, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(serialAttachment)
		if err != nil {
			return fmt.Errorf("serial port: %w", err)
		}
		vmConfig.SetSerialPortsVirtualMachineConfiguration([]*vz.VirtioConsoleDeviceSerialPortConfiguration{serialPort})

		validated, err := vmConfig.Validate()
		if err != nil {
			return fmt.Errorf("validate config: %w", err)
		}
		if !validated {
			return fmt.Errorf("vm config validation failed")
		}

		vm, err = vz.NewVirtualMachine(vmConfig)
		if err != nil {
			return fmt.Errorf("new VM: %w", err)
		}

		if err := vm.Start(); err != nil {
			return fmt.Errorf("start VM: %w", err)
		}

		socketDevices := vm.SocketDevices()
		if len(socketDevices) == 0 {
			return fmt.Errorf("no vsock devices on VM")
		}
		vsockDev = socketDevices[0]

		return nil
	})

	if err != nil {
		r.imageMgr.CleanupInstance(handle)
		return "", err
	}

	agentClient, err := agentclient.Dial(ctx, func() (net.Conn, error) {
		return vsockDev.Connect(uint32(microvm.AgentVsockPort))
	})
	if err != nil {
		r.runOnVMThread(func() error {
			vm.Stop()
			return nil
		})
		r.imageMgr.CleanupInstance(handle)
		return "", fmt.Errorf("dial agent: %w", err)
	}

	inst := &vmInstance{
		vm:       vm,
		agent:    agentClient,
		rootfs:   instanceRootfs,
		vsockDev: vsockDev,
	}

	r.mu.Lock()
	r.instances[handle] = inst
	r.mu.Unlock()

	log.Printf("applevm: provisioned %s", handle)
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

	log.Printf("applevm: destroying %s", handle)

	if inst.agent != nil {
		inst.agent.Close()
	}
	if inst.vm != nil {
		_ = r.runOnVMThread(func() error {
			return inst.vm.Stop()
		})
	}
	r.imageMgr.CleanupInstance(handle)
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
	close(r.vmOps)
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
