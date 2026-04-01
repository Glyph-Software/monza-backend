//go:build linux

package firecracker

import (
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"sync"
)

// ipAllocator hands out /30 subnets from a larger block for point-to-point
// links between the host TAP and the guest VM.
type ipAllocator struct {
	mu       sync.Mutex
	baseIP   net.IP
	maskBits int
	next     uint32
	used     map[string]uint32 // handle -> offset
}

func newIPAllocator(cidr string) (*ipAllocator, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}
	ones, _ := ipNet.Mask.Size()
	return &ipAllocator{
		baseIP:   ip.To4(),
		maskBits: ones,
		next:     0,
		used:     make(map[string]uint32),
	}, nil
}

// Allocate returns a host IP and guest IP for a /30 point-to-point link.
func (a *ipAllocator) Allocate(handle string) (hostIP, guestIP net.IP, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if off, ok := a.used[handle]; ok {
		return a.ipAt(off), a.ipAt(off + 1), nil
	}

	offset := a.next
	a.next += 4 // each /30 uses 4 addresses (network, host, guest, broadcast)
	a.used[handle] = offset + 1

	return a.ipAt(offset + 1), a.ipAt(offset + 2), nil
}

// Release frees the subnet allocated for handle.
func (a *ipAllocator) Release(handle string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, handle)
}

func (a *ipAllocator) ipAt(offset uint32) net.IP {
	base := binary.BigEndian.Uint32(a.baseIP)
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, base+offset)
	return ip
}

// setupTAP creates a TAP device and assigns the host IP.
func setupTAP(name string, hostIP net.IP) error {
	if err := run("ip", "tuntap", "add", "dev", name, "mode", "tap"); err != nil {
		return fmt.Errorf("create TAP %s: %w", name, err)
	}
	if err := run("ip", "addr", "add", hostIP.String()+"/30", "dev", name); err != nil {
		teardownTAP(name)
		return fmt.Errorf("assign IP to %s: %w", name, err)
	}
	if err := run("ip", "link", "set", name, "up"); err != nil {
		teardownTAP(name)
		return fmt.Errorf("bring up %s: %w", name, err)
	}

	_ = run("sysctl", "-w", "net.ipv4.ip_forward=1")
	_ = run("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE")
	_ = run("iptables", "-A", "FORWARD", "-i", name, "-j", "ACCEPT")
	_ = run("iptables", "-A", "FORWARD", "-o", name, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")

	return nil
}

// teardownTAP removes a TAP device.
func teardownTAP(name string) {
	_ = run("iptables", "-D", "FORWARD", "-i", name, "-j", "ACCEPT")
	_ = run("iptables", "-D", "FORWARD", "-o", name, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	_ = run("ip", "link", "delete", name)
}

// generateMAC creates a locally-administered MAC address from a CID.
func generateMAC(cid uint32) string {
	return fmt.Sprintf("02:FC:00:00:%02X:%02X", byte(cid>>8), byte(cid))
}

func run(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
