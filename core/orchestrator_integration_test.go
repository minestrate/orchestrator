//go:build integration

package core

import (
	"context"
	"io"
	"testing"
	"time"

	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func TestDockerIntegration(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker not available, skipping integration test: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Docker daemon not reachable, skipping integration test: %v", err)
	}

	image := "nginx:alpine"
	t.Logf("pulling %s...", image)
	pullResp, err := cli.ImagePull(ctx, image, dockerimage.PullOptions{})
	if err != nil {
		t.Fatalf("failed to pull image %s: %v", image, err)
	}
	io.Copy(io.Discard, pullResp)
	pullResp.Close()

	cfg := &Config{}
	cfg.Docker.Image = image
	cfg.Ports.RangeStart = 30000
	cfg.Ports.RangeEnd = 30010
	cfg.Orchestrator.MaxServers = 5
	cfg.Orchestrator.Workers = 1
	cfg.Orchestrator.StartTimeout = 60
	cfg.Network.DefaultNetwork = "bridge"

	o, err := NewOrchestrator(cfg, cli)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}
	o.StartWorkers()

	s, err := o.CreateServer(context.Background(), CreateServerOptions{
		Game:    "test",
		Players: 4,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	t.Logf("created server: %s", s.ID)

	// Wait for the worker to start the container.
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		state := s.State()
		t.Logf("server state: %s", state)
		if state == "running" {
			break
		}
		if state == "stopped" {
			t.Fatal("server transitioned to stopped — container start likely failed")
		}
	}

	insp, err := cli.ContainerInspect(context.Background(), s.ContainerName())
	if err != nil {
		t.Fatalf("container not found: %v", err)
	}
	if !insp.State.Running {
		t.Fatal("container should be running")
	}
	t.Logf("container running: %s", s.ContainerName())

	// Clean up.
	if err := o.StopServer(context.Background(), s.ID); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	time.Sleep(1 * time.Second)
	_, err = cli.ContainerInspect(context.Background(), s.ContainerName())
	if err == nil {
		t.Fatal("container should have been removed")
	}
	t.Log("integration test passed")
}
