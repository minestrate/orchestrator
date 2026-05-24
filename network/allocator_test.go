package network

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type mockDockerClient struct {
	mu       sync.Mutex
	networks map[string]string // name -> subnet
}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.networks[name]; ok {
		return network.CreateResponse{}, fmt.Errorf("network %s already exists", name)
	}
	m.networks[name] = options.IPAM.Config[0].Subnet
	return network.CreateResponse{ID: name}, nil
}

func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.networks[networkID]; !ok {
		return fmt.Errorf("network %s not found", networkID)
	}
	delete(m.networks, networkID)
	return nil
}

func (m *mockDockerClient) NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
	return network.Inspect{}, nil
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	return container.CreateResponse{ID: containerName}, nil
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	return nil
}

func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	return nil
}

func (m *mockDockerClient) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	return nil
}

func TestNetworkAllocator_Disjoint(t *testing.T) {
	mock := &mockDockerClient{networks: make(map[string]string)}
	// Smaller block for testing: /24 partitioned into /28s (16 subnets)
	block := "192.168.1.0/24"
	alloc, err := NewIsolatedSubnetManager(mock, block)
	if err != nil {
		t.Fatalf("Failed to create NetworkAllocator: %v", err)
	}

	subnets := make([]*net.IPNet, 0)
	for i := 0; i < 16; i++ {
		cfg, err := alloc.Allocate(context.Background(), fmt.Sprintf("game-%d", i))
		if err != nil {
			t.Fatalf("Acquire failed at %d: %v", i, err)
		}
		_, ipnet, _ := net.ParseCIDR(cfg.Subnet)
		subnets = append(subnets, ipnet)
	}

	// Verify all are disjoint
	for i := 0; i < len(subnets); i++ {
		for j := i + 1; j < len(subnets); j++ {
			if overlaps(subnets[i], subnets[j]) {
				t.Errorf("Subnets overlap: %s and %s", subnets[i], subnets[j])
			}
		}
	}
}

func overlaps(n1, n2 *net.IPNet) bool {
	return n1.Contains(n2.IP) || n2.Contains(n1.IP)
}

func TestNetworkAllocator_Concurrency(t *testing.T) {
	mock := &mockDockerClient{networks: make(map[string]string)}
	block := "172.20.0.0/14"
	alloc, err := NewIsolatedSubnetManager(mock, block)
	if err != nil {
		t.Fatalf("Failed to create NetworkAllocator: %v", err)
	}

	const numWorkers = 100
	results := make(chan string, numWorkers)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cfg, err := alloc.Allocate(context.Background(), fmt.Sprintf("game-%d", id))
			if err != nil {
				t.Errorf("Acquire failed: %v", err)
				return
			}
			results <- cfg.Subnet
		}(i)
	}

	wg.Wait()
	close(results)

	seen := make(map[string]bool)
	for s := range results {
		if seen[s] {
			t.Errorf("Duplicate subnet allocated: %s", s)
		}
		seen[s] = true
	}

	if len(seen) != numWorkers {
		t.Errorf("Expected %d unique subnets, got %d", numWorkers, len(seen))
	}
}

func TestNetworkAllocator_Exhaustion(t *testing.T) {
	mock := &mockDockerClient{networks: make(map[string]string)}
	block := "192.168.1.0/30" // Only one /28 doesn't fit, so partitionSubnet should fail or handle it.
	// Wait, /30 is smaller than /28. partitionSubnet should error.
	_, err := NewIsolatedSubnetManager(mock, block)
	if err == nil {
		t.Fatal("Expected error for block smaller than /28")
	}

	block = "192.168.1.0/27" // Two /28s (192.168.1.0/28 and 192.168.1.16/28)
	alloc, err := NewIsolatedSubnetManager(mock, block)
	if err != nil {
		t.Fatalf("Failed to create NetworkAllocator: %v", err)
	}

	_, _ = alloc.Allocate(context.Background(), "game-1")
	_, _ = alloc.Allocate(context.Background(), "game-2")
	_, err = alloc.Allocate(context.Background(), "game-3")
	if err != ErrNoSubnetsAvailable {
		t.Errorf("Expected ErrNoSubnetsAvailable, got %v", err)
	}
}

func TestNetworkAllocator_Release(t *testing.T) {
	mock := &mockDockerClient{networks: make(map[string]string)}
	block := "192.168.1.0/28"
	alloc, err := NewIsolatedSubnetManager(mock, block)
	if err != nil {
		t.Fatalf("Failed to create NetworkAllocator: %v", err)
	}

	gameID := "test-game"
	cfg, err := alloc.Allocate(context.Background(), gameID)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if len(mock.networks) != 1 {
		t.Errorf("Expected 1 network in mock, got %d", len(mock.networks))
	}

	err = alloc.Release(context.Background(), gameID)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	if len(mock.networks) != 0 {
		t.Errorf("Expected 0 networks in mock after release, got %d", len(mock.networks))
	}

	cfg2, err := alloc.Allocate(context.Background(), gameID)
	if err != nil {
		t.Fatalf("Acquire failed after release: %v", err)
	}
	if cfg.Subnet != cfg2.Subnet {
		t.Errorf("Expected same subnet to be reused, got %s", cfg2.Subnet)
	}
}

func TestPortAllocator_Concurrency(t *testing.T) {
	start, end := 20000, 30000
	alloc := NewPortAllocator(start, end)

	const numWorkers = 1000
	ports := make(chan int, numWorkers)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			port, err := alloc.Acquire()
			if err != nil {
				t.Errorf("Acquire failed: %v", err)
				return
			}
			ports <- port
		}()
	}

	wg.Wait()
	close(ports)

	seen := make(map[int]bool)
	for port := range ports {
		if seen[port] {
			t.Errorf("Duplicate port allocated: %d", port)
		}
		seen[port] = true
		if port < start || port > end {
			t.Errorf("Port out of range: %d", port)
		}
	}

	if len(seen) != numWorkers {
		t.Errorf("Expected %d unique ports, got %d", numWorkers, len(seen))
	}
}

func TestPortAllocator_Exhaustion(t *testing.T) {
	start, end := 20000, 20005
	alloc := NewPortAllocator(start, end)

	for i := 0; i < 6; i++ {
		_, err := alloc.Acquire()
		if err != nil {
			t.Fatalf("Expected port, got error: %v", err)
		}
	}

	_, err := alloc.Acquire()
	if err != ErrNoPortsAvailable {
		t.Errorf("Expected ErrNoPortsAvailable, got %v", err)
	}
}

func TestPortAllocator_Release(t *testing.T) {
	start, end := 20000, 20000
	alloc := NewPortAllocator(start, end)

	port, err := alloc.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	alloc.Release(port)
	// Idempotency check
	alloc.Release(port)

	port2, err := alloc.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed after release: %v", err)
	}
	if port != port2 {
		t.Errorf("Expected same port to be reused, got %d", port2)
	}
}
