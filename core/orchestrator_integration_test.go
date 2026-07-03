//go:build integration

package core

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

func TestDockerIntegration(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker not available, skipping integration test: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not reachable, skipping integration test: %v", err)
	}

	cfg := &Config{}
	cfg.Docker.Image = "nginx:alpine"
	cfg.Ports.RangeStart = 30000
	cfg.Ports.RangeEnd = 30010
	cfg.Orchestrator.MaxServers = 5
	cfg.Orchestrator.Workers = 2
	cfg.Orchestrator.StartTimeout = 30
	cfg.Network.DefaultNetwork = "minestrate-test"

	o, err := NewOrchestrator(cfg, cli)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}
	o.StartWorkers()

	// Create a server.
	s, err := o.CreateServer(context.Background(), CreateServerOptions{
		Game:    "test",
		Players: 4,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Logf("created server: %s", s.ID)

	// Wait for the container to start.
	time.Sleep(5 * time.Second)

	// Verify container exists.
	insp, err := cli.ContainerInspect(context.Background(), s.ContainerName())
	if err != nil {
		t.Fatalf("container not found after creation: %v", err)
	}
	if !insp.State.Running {
		t.Fatal("container should be running")
	}

	// Clean up.
	if err := o.StopServer(context.Background(), s.ID); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	// Verify container removed.
	_, err = cli.ContainerInspect(context.Background(), s.ContainerName())
	if err == nil {
		t.Fatal("container should have been removed")
	}
}
