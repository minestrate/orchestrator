package allocator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/docker/docker/api/types/network"
	"github.com/mitsuakki/minestrate/core/dockerclient"
)

var (
	ErrNoSubnetsAvailable = errors.New("no subnets available")
	ErrNetworkNotFound    = errors.New("network not found")
	ErrInvalidNetworkMode = errors.New("invalid network mode")
)

type NetworkConfig struct {
	NetworkName string `json:"network_name"`
	Subnet      string `json:"subnet"`
	Gateway     string `json:"gateway"`
}

type NetworkManager interface {
	Allocate(ctx context.Context, gameID string) (*NetworkConfig, error)
	Release(ctx context.Context, gameID string) error
	ListActive() map[string]*NetworkConfig
}

// SimpleNetworkManager implements a shared global network mode.
type SimpleNetworkManager struct {
	networkName string
	active      map[string]*NetworkConfig
	mu          sync.RWMutex
}

func NewSimpleNetworkManager(networkName string) *SimpleNetworkManager {
	return &SimpleNetworkManager{
		networkName: networkName,
		active:      make(map[string]*NetworkConfig),
	}
}

func EnsureNetwork(ctx context.Context, docker dockerclient.Client, name string) error {
	_, err := docker.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = docker.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	return err
}

func (m *SimpleNetworkManager) Allocate(ctx context.Context, gameID string) (*NetworkConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg := &NetworkConfig{
		NetworkName: m.networkName,
	}
	m.active[gameID] = cfg
	return cfg, nil
}

func (m *SimpleNetworkManager) Release(ctx context.Context, gameID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, gameID)
	return nil
}

func (m *SimpleNetworkManager) ListActive() map[string]*NetworkConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make(map[string]*NetworkConfig)
	for k, v := range m.active {
		res[k] = v
	}
	return res
}

// IsolatedSubnetManager implements dynamic isolated subnet mode.
type IsolatedSubnetManager struct {
	docker      dockerclient.Client
	baseSubnet  *net.IPNet
	subnets     []*net.IPNet
	subnetToIdx map[string]int
	bits        []uint64
	active      map[string]*NetworkConfig
	idToSubnet  map[string]string
	mu          sync.RWMutex
}

func NewIsolatedSubnetManager(docker dockerclient.Client, subnetBlock string) (*IsolatedSubnetManager, error) {
	_, ipnet, err := net.ParseCIDR(subnetBlock)
	if err != nil {
		return nil, err
	}

	// Reject IPv6 — partitionSubnet uses int shifts that overflow for 128-bit addresses.
	if ipnet.IP.To4() == nil {
		return nil, fmt.Errorf("subnet_block must be an IPv4 CIDR, got %q", subnetBlock)
	}

	subnets, err := partitionSubnet(ipnet, 28)
	if err != nil {
		return nil, err
	}

	numUint64 := (len(subnets) + 63) / 64
	bits := make([]uint64, numUint64)

	subnetToIdx := make(map[string]int)
	for i, s := range subnets {
		subnetToIdx[s.String()] = i
	}

	// Mark bits beyond len(subnets) as reserved
	if len(subnets)%64 != 0 {
		lastIdx := numUint64 - 1
		remainingBits := len(subnets) % 64
		var mask uint64 = ^((1 << remainingBits) - 1)
		bits[lastIdx] = mask
	}

	return &IsolatedSubnetManager{
		docker:      docker,
		baseSubnet:  ipnet,
		subnets:     subnets,
		subnetToIdx: subnetToIdx,
		bits:        bits,
		active:      make(map[string]*NetworkConfig),
		idToSubnet:  make(map[string]string),
	}, nil
}

func (m *IsolatedSubnetManager) Allocate(ctx context.Context, gameID string) (*NetworkConfig, error) {
	for i := 0; i < len(m.bits); i++ {
		for {
			val := atomic.LoadUint64(&m.bits[i])
			if val == ^uint64(0) {
				break
			}

			bitIdx := -1
			for j := 0; j < 64; j++ {
				if (val & (1 << j)) == 0 {
					bitIdx = j
					break
				}
			}

			if bitIdx == -1 {
				break
			}

			newVal := val | (1 << bitIdx)
			if atomic.CompareAndSwapUint64(&m.bits[i], val, newVal) {
				idx := i*64 + bitIdx
				subnet := m.subnets[idx].String()
				name := fmt.Sprintf("minestrate-net-%d", idx)

				_, err := m.docker.NetworkCreate(ctx, name, network.CreateOptions{
					Driver: "bridge",
					IPAM: &network.IPAM{
						Config: []network.IPAMConfig{
							{
								Subnet: subnet,
							},
						},
					},
				})
				if err != nil {
					m.releaseIndex(idx)
					return nil, err
				}

				cfg := &NetworkConfig{
					NetworkName: name,
					Subnet:      subnet,
				}

				m.mu.Lock()
				m.active[gameID] = cfg
				m.idToSubnet[gameID] = subnet
				m.mu.Unlock()

				return cfg, nil
			}
		}
	}
	return nil, ErrNoSubnetsAvailable
}

func (m *IsolatedSubnetManager) Release(ctx context.Context, gameID string) error {
	m.mu.Lock()
	subnet, ok := m.idToSubnet[gameID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.active, gameID)
	delete(m.idToSubnet, gameID)
	m.mu.Unlock()

	idx, ok := m.subnetToIdx[subnet]
	if !ok {
		return nil
	}

	name := fmt.Sprintf("minestrate-net-%d", idx)
	if err := m.docker.NetworkRemove(ctx, name); err != nil {
		return err // keep the bit reserved so the caller can retry
	}

	m.releaseIndex(idx)
	return nil
}

func (m *IsolatedSubnetManager) ListActive() map[string]*NetworkConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make(map[string]*NetworkConfig)
	for k, v := range m.active {
		res[k] = v
	}
	return res
}

func (m *IsolatedSubnetManager) releaseIndex(idx int) {
	i := idx / 64
	bitIdx := idx % 64
	mask := uint64(1) << bitIdx

	for {
		val := atomic.LoadUint64(&m.bits[i])
		if (val & mask) == 0 {
			return
		}
		newVal := val & ^mask
		if atomic.CompareAndSwapUint64(&m.bits[i], val, newVal) {
			return
		}
	}
}

// FallbackNetworkManager attempts isolated allocation, falling back to simple.
type FallbackNetworkManager struct {
	primary   NetworkManager
	secondary NetworkManager
	mu        sync.Mutex
	isPrimary map[string]bool
}

func NewFallbackNetworkManager(primary, secondary NetworkManager) *FallbackNetworkManager {
	return &FallbackNetworkManager{
		primary:   primary,
		secondary: secondary,
		isPrimary: make(map[string]bool),
	}
}

func (m *FallbackNetworkManager) Allocate(ctx context.Context, gameID string) (*NetworkConfig, error) {
	cfg, err := m.primary.Allocate(ctx, gameID)
	if err == nil {
		m.mu.Lock()
		m.isPrimary[gameID] = true
		m.mu.Unlock()
		return cfg, nil
	}

	cfg, err = m.secondary.Allocate(ctx, gameID)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.isPrimary[gameID] = false
	m.mu.Unlock()
	return cfg, nil
}

func (m *FallbackNetworkManager) Release(ctx context.Context, gameID string) error {
	m.mu.Lock()
	primary := m.isPrimary[gameID]
	m.mu.Unlock()

	var err error
	if primary {
		err = m.primary.Release(ctx, gameID)
	} else {
		err = m.secondary.Release(ctx, gameID)
	}
	if err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.isPrimary, gameID)
	m.mu.Unlock()
	return nil
}

func (m *FallbackNetworkManager) ListActive() map[string]*NetworkConfig {
	res := m.primary.ListActive()
	secondary := m.secondary.ListActive()
	for k, v := range secondary {
		res[k] = v
	}
	return res
}

func partitionSubnet(base *net.IPNet, newMask int) ([]*net.IPNet, error) {
	ones, bits := base.Mask.Size()
	if newMask < ones {
		return nil, fmt.Errorf("new mask %d must be greater than or equal to base mask %d", newMask, ones)
	}

	numSubnets := 1 << (newMask - ones)
	subnets := make([]*net.IPNet, 0, numSubnets)

	currentIP := make(net.IP, len(base.IP))
	copy(currentIP, base.IP)

	subnetSize := 1 << (bits - newMask)

	for i := 0; i < numSubnets; i++ {
		subnetIP := make(net.IP, len(currentIP))
		copy(subnetIP, currentIP)
		subnets = append(subnets, &net.IPNet{
			IP:   subnetIP,
			Mask: net.CIDRMask(newMask, bits),
		})

		incrementIP(currentIP, subnetSize)
	}

	return subnets, nil
}

func incrementIP(ip net.IP, inc int) {
	for i := len(ip) - 1; i >= 0; i-- {
		sum := int(ip[i]) + inc
		ip[i] = byte(sum)
		inc = sum >> 8
		if inc == 0 {
			break
		}
	}
}
