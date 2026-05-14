package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/go-chi/chi/v5"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/internal/api"
	"github.com/mitsuakki/minestrate/internal/server"
)

type mockDockerClient struct{}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateRequest) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}

func TestServerLifecycle_Integration(t *testing.T) {
	// Note: The address returned is the host IP, not the container IP.
	// Note: Port is reserved at enqueue time, not at container start.

	// Setup
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 2
	cfg.Ports.RangeStart = 20000
	cfg.Ports.RangeEnd = 20100
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"

	orchestrator, err := server.NewOrchestrator(cfg, &mockDockerClient{})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	orchestrator.StartWorkers()
	h := api.NewHandler(orchestrator)

	r := chi.NewRouter()
	r.Post("/servers", h.CreateServer)
	r.Get("/servers/{id}", h.GetServer)

	ts := httptest.NewServer(r)
	defer ts.Close()

	// 1. POST /servers
	reqBody := api.CreateServerRequest{
		Game:    "survival",
		Players: 20,
	}
	body, _ := json.Marshal(reqBody)
	
	resp, err := http.Post(ts.URL+"/servers", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("Failed to POST /servers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected status 202, got %d", resp.StatusCode)
	}

	var createdServer struct {
		ID      string `json:"id"`
		Port    int    `json:"port"`
		Address string `json:"address"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createdServer); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if createdServer.ID == "" {
		t.Fatal("Expected server ID, got empty string")
	}

	// Verify port is reserved at enqueue time
	if createdServer.Port < 20000 || createdServer.Port > 20100 {
		t.Errorf("Expected port in range 20000-20100, got %d", createdServer.Port)
	}

	// Verify address is host IP (mocked as 127.0.0.1 in current implementation)
	// Note: The requirement says "The address returned is the host IP, not the container IP."
	if createdServer.Address == "" {
		t.Error("Expected host address, got empty string")
	}

	// 2. Poll GET /servers/{id} until running
	id := createdServer.ID
	maxAttempts := 20
	success := false
	
	for i := 0; i < maxAttempts; i++ {
		resp, err := http.Get(fmt.Sprintf("%s/servers/%s", ts.URL, id))
		if err != nil {
			t.Fatalf("Failed to GET /servers/%s: %v", id, err)
		}
		
		var polledServer struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&polledServer); err != nil {
			resp.Body.Close()
			t.Fatalf("Failed to decode polled response: %v", err)
		}
		resp.Body.Close()

		if polledServer.State == string(server.StateRunning) {
			success = true
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Errorf("Server %s did not reach running state within timeout", id)
	}
}
