//go:build linux

package firecracker

import (
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
)

// tapNameFromHandle returns a Linux netdev name (max 15 chars) that stays
// unique per sandbox. Truncating "tap-"+fullHandle to 15 bytes makes every
// "my-python-sandbox-<id>" collide as "tap-my-python-s".
func tapNameFromHandle(handle string) string {
	const maxLen = 15
	suffix := handle
	if i := strings.LastIndex(handle, "-"); i >= 0 && i+1 < len(handle) {
		suffix = strings.TrimSpace(handle[i+1:])
	}
	var b strings.Builder
	for _, r := range suffix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
		if b.Len() >= 10 {
			break
		}
	}
	suf := b.String()
	if suf == "" {
		suf = "0"
	}
	name := "tap-" + suf
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

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
	// Remove stale interface from a crashed prior run (ignore errors).
	_, _ = exec.Command("ip", "link", "delete", name).CombinedOutput()

	out, err := exec.Command("ip", "tuntap", "add", "dev", name, "mode", "tap").CombinedOutput()
	if err != nil {
		return fmt.Errorf("create TAP %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	out, err = exec.Command("ip", "addr", "add", hostIP.String()+"/30", "dev", name).CombinedOutput()
	if err != nil {
		teardownTAP(name)
		return fmt.Errorf("assign IP to %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	out, err = exec.Command("ip", "link", "set", name, "up").CombinedOutput()
	if err != nil {
		teardownTAP(name)
		return fmt.Errorf("bring up %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}

	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	wan := defaultWANInterface()
	if wan != "" {
		// One MASQUERADE rule per WAN iface; avoid stacking duplicates per sandbox.
		if err := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-o", wan, "-j", "MASQUERADE").Run(); err != nil {
			_, _ = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-o", wan, "-j", "MASQUERADE").CombinedOutput()
		}
	}
	_, _ = exec.Command("iptables", "-A", "FORWARD", "-i", name, "-j", "ACCEPT").CombinedOutput()
	_, _ = exec.Command("iptables", "-A", "FORWARD", "-o", name, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").CombinedOutput()

	return nil
}

// defaultWANInterface returns the interface used for the default route (e.g.
// ens5 on EC2), or empty if unknown. Falls back so MASQUERADE is not applied
// to a non-existent "eth0".
func defaultWANInterface() string {
	out, err := exec.Command("ip", "-4", "route", "show", "default").CombinedOutput()
	if err != nil {
		return "eth0"
	}
	// Typical: "default via 172.31.0.1 dev ens5 proto dhcp ..."
	fields := strings.Fields(string(out))
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return "eth0"
}

// teardownTAP removes a TAP device.
func teardownTAP(name string) {
	_, _ = exec.Command("iptables", "-D", "FORWARD", "-i", name, "-j", "ACCEPT").CombinedOutput()
	_, _ = exec.Command("iptables", "-D", "FORWARD", "-o", name, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").CombinedOutput()
	_, _ = exec.Command("ip", "link", "delete", name).CombinedOutput()
}

// generateMAC creates a locally-administered MAC address from a CID.
func generateMAC(cid uint32) string {
	return fmt.Sprintf("02:FC:00:00:%02X:%02X", byte(cid>>8), byte(cid))
}
