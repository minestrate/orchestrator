package http_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mitsuakki/minestrate/api/http"
	"github.com/mitsuakki/minestrate/api/service"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/domain"
	"github.com/mitsuakki/minestrate/orchestrator"
)

func TestServerLifecycle_Integration(t *testing.T) {
	// Setup
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 2
	cfg.Orchestrator.StartTimeout = 30
	cfg.Ports.RangeStart = 20000
	cfg.Ports.RangeEnd = 20100
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"

	orchestratorInstance, err := orchestrator.NewOrchestrator(cfg, &orchestrator.MockDockerClient{})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	orchestratorInstance.StartWorkers()
	refreshManager := service.NewRefreshManager("test-secret")
	h := http.NewHandler(orchestratorInstance, refreshManager)

	r := chi.NewRouter()
	r.Post("/servers", h.CreateServer)
	r.Get("/servers/{id}", h.GetServer)

	ts := httptest.NewServer(r)
	defer ts.Close()

	// 1. POST /servers
	reqBody := http.CreateServerRequest{
		Game:    "survival",
		Players: 20,
	}
	body, _ := json.Marshal(reqBody)

	resp, err := nethttp.Post(ts.URL+"/servers", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("Failed to POST /servers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != nethttp.StatusAccepted {
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

	// 2. Poll GET /servers/{id} until running
	id := createdServer.ID
	maxAttempts := 20
	success := false

	for i := 0; i < maxAttempts; i++ {
		resp, err := nethttp.Get(fmt.Sprintf("%s/servers/%s", ts.URL, id))
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

		if polledServer.State == string(domain.StateRunning) {
			success = true
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Errorf("Server %s did not reach running state within timeout", id)
	}
}
