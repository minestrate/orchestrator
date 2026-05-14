package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mitsuakki/minestrate/config"
)

func mockConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 2
	cfg.Orchestrator.Workers = 1
	cfg.Ports.RangeStart = 25565
	cfg.Ports.RangeEnd = 25566
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"
	return cfg
}

func TestNewOrchestrator(t *testing.T) {
	cfg := mockConfig()
	o, err := NewOrchestrator(cfg, &mockDockerClient{})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	if o == nil {
		t.Fatal("expected orchestrator to be non-nil")
	}
	if o.cfg != cfg {
		t.Fatal("expected config to be set")
	}
	if o.ports == nil {
		t.Fatal("expected port allocator to be initialized")
	}
}

func TestCreateServer(t *testing.T) {
	cfg := mockConfig()
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})

	s1, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}
	if s1 == nil {
		t.Fatal("expected server to be non-nil")
	}
	if s1.Game != "minecraft" || s1.Players != 10 || s1.Port != 25565 {
		t.Fatalf("server properties mismatch: %+v", s1)
	}

	s2, err := o.CreateServer(context.Background(), "minecraft", 5)
	if err != nil {
		t.Fatalf("unexpected error creating second server: %v", err)
	}
	if s2.Port != 25566 {
		t.Fatalf("expected port 25566, got %d", s2.Port)
	}

	// Max servers reached
	s3, err := o.CreateServer(context.Background(), "minecraft", 5)
	if !errors.Is(err, ErrMaxServersReached) {
		t.Fatalf("expected ErrMaxServersReached, got %v", err)
	}
	if s3 != nil {
		t.Fatal("expected nil server when max servers reached")
	}
}

func TestCreateServer_NoPorts(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.MaxServers = 5
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})

	_, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("unexpected error creating server 1: %v", err)
	}
	_, err = o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("unexpected error creating server 2: %v", err)
	}

	// No ports available
	s3, err := o.CreateServer(context.Background(), "minecraft", 10)
	if !errors.Is(err, ErrNoPortsAvailable) {
		t.Fatalf("expected ErrNoPortsAvailable, got %v", err)
	}
	if s3 != nil {
		t.Fatal("expected nil server when no ports available")
	}
}

func TestGetAndListServers(t *testing.T) {
	cfg := mockConfig()
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})

	s1, _ := o.CreateServer(context.Background(), "minecraft", 10)
	
	s, found := o.GetServer(s1.ID)
	if !found {
		t.Fatal("expected to find server")
	}
	if s != s1 {
		t.Fatal("server mismatch")
	}

	s, found = o.GetServer("non-existent")
	if found {
		t.Fatal("expected not to find non-existent server")
	}
	if s != nil {
		t.Fatal("expected nil for non-existent server")
	}

	list := o.ListServers()
	if len(list) != 1 {
		t.Fatalf("expected 1 server in list, got %d", len(list))
	}
	if list[0] != s1 {
		t.Fatal("server in list mismatch")
	}
}

func TestCreateServer_RaceCondition(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.MaxServers = 1
	cfg.Ports.RangeEnd = 25570 // Enough ports
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})

	const numGoroutines = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errs := make(chan error, numGoroutines)
	servers := make(chan *Server, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			s, err := o.CreateServer(context.Background(), "minecraft", 10)
			if err != nil {
				errs <- err
				return
			}
			servers <- s
		}()
	}

	wg.Wait()
	close(errs)
	close(servers)

	createdCount := len(servers)
	if createdCount > cfg.Orchestrator.MaxServers {
		t.Errorf("Exceeded MaxServers: created %d, limit %d", createdCount, cfg.Orchestrator.MaxServers)
	}
}

func TestCreateServer_Backpressure(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 0 // No workers to drain the queue
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})
	
	// Set a small job queue for testing
	o.jobQueue = make(chan *Server, 1)

	// Fill the queue
	_, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("Failed to create first server: %v", err)
	}

	// Try to create another one, should return error instead of blocking
	errChan := make(chan error, 1)
	go func() {
		_, err := o.CreateServer(context.Background(), "minecraft", 10)
		errChan <- err
	}()

	select {
	case err := <-errChan:
		if !errors.Is(err, ErrJobQueueFull) {
			t.Fatalf("expected ErrJobQueueFull, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):

		t.Fatal("CreateServer blocked instead of returning error")
	}
}

func TestMultipleWorkers(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.Workers = 2
	cfg.Orchestrator.MaxServers = 5
	cfg.Ports.RangeEnd = 25570
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})
	o.StartWorkers()

	s1, _ := o.CreateServer(context.Background(), "game1", 10)
	s2, _ := o.CreateServer(context.Background(), "game2", 10)

	// Wait for workers to process.
	time.Sleep(250 * time.Millisecond)

	if s1.State() != StateRunning {
		t.Fatalf("s1: expected running, got %s", s1.State())
	}
	if s2.State() != StateRunning {
		t.Fatalf("s2: expected running, got %s", s2.State())
	}
}

func TestStopRunningServer(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.Workers = 1
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})
	o.StartWorkers()

	s, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Wait for worker to advance state to running
	time.Sleep(200 * time.Millisecond)

	if s.State() != StateRunning {
		t.Fatalf("expected state running, got %s", s.State())
	}

	err = o.StopServer(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("expected StopServer to succeed for running server, got error: %v", err)
	}

	if s.State() != StateStopped {
		t.Fatalf("expected state stopped, got %s", s.State())
	}
}

func TestStopStartingServer(t *testing.T) {
	cfg := mockConfig()
	cfg.Orchestrator.Workers = 1
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})
	// Don't start workers yet, so we can control the transition

	s, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Manually transition to starting
	err = s.Transition(EventStart)
	if err != nil {
		t.Fatalf("failed to transition to starting: %v", err)
	}

	if s.State() != StateStarting {
		t.Fatalf("expected state starting, got %s", s.State())
	}

	err = o.StopServer(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("expected StopServer to succeed for starting server, got error: %v", err)
	}

	if s.State() != StateStopped {
		t.Fatalf("expected state stopped, got %s", s.State())
	}
}

func TestStopServer_Inconsistency(t *testing.T) {
	cfg := mockConfig()
	o, _ := NewOrchestrator(cfg, &mockDockerClient{})

	s, err := o.CreateServer(context.Background(), "minecraft", 10)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Manually transition to Stopped state.
	// We use a trick: transition to Stopped makes future EventStop fail.
	// But we need to keep it in the orchestrator.
	err = s.Transition(EventStop) 
	if err != nil {
		t.Fatalf("failed to transition to stopped: %v", err)
	}
	
	// Now it's stopped, but still in o.servers (because we didn't use StopServer yet).
	// Calling StopServer should fail because Stopped -> Stopped via EventStop is invalid.
	err = o.StopServer(context.Background(), s.ID)
	if err == nil {
		t.Fatal("expected StopServer to fail for already stopped server")
	}

	// Verify inconsistency: Is it still in the orchestrator?
	_, found := o.GetServer(s.ID)
	if !found {
		t.Error("BUG: server was removed from orchestrator even though StopServer failed")
	}
}
