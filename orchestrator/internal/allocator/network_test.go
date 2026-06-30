package allocator

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/network"
)

type mockDockerClient struct{}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}
func (m *mockDockerClient) NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
	return network.Inspect{}, nil
}

func TestSimpleNetworkManager(t *testing.T) {
	nm := NewSimpleNetworkManager("test-net")
	ctx := context.Background()

	cfg, err := nm.Allocate(ctx, "game-1")
	if err != nil {
		t.Fatalf("failed to allocate: %v", err)
	}
	if cfg.NetworkName != "test-net" {
		t.Fatalf("expected test-net, got %s", cfg.NetworkName)
	}

	active := nm.ListActive()
	if len(active) != 1 || active["game-1"] != cfg {
		t.Fatalf("list active mismatch")
	}

	if err := nm.Release(ctx, "game-1"); err != nil {
		t.Fatalf("failed to release: %v", err)
	}
	if len(nm.ListActive()) != 0 {
		t.Fatalf("expected no active subnets")
	}
}
