package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/internal/server"
)

type mockDockerClient struct{}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
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

func setupTestHandler() *Handler {
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 1
	cfg.Ports.RangeStart = 19132
	cfg.Ports.RangeEnd = 19142
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"

	o, err := server.NewOrchestrator(cfg, &mockDockerClient{})
	if err != nil {
		panic(err)
	}
	return NewHandler(o)
}

func TestCreateServer(t *testing.T) {
	h := setupTestHandler()
	
	t.Run("ValidRequest", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "skywars", Players: 8}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		res := w.Result()
		defer res.Body.Close()

		if res.StatusCode != http.StatusAccepted {
			t.Fatalf("expected status %d, got %d", http.StatusAccepted, res.StatusCode)
		}

		var respBody ServerResponse
		if err := json.NewDecoder(res.Body).Decode(&respBody); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if respBody.Game != "skywars" || respBody.Players != 8 {
			t.Fatalf("unexpected response body: %+v", respBody)
		}
	})

	t.Run("InvalidGame", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "", Players: 8}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		if w.Result().StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Result().StatusCode)
		}
	})

	t.Run("InvalidPlayers", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "skywars", Players: 0}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		if w.Result().StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Result().StatusCode)
		}
	})
}

func TestListServers(t *testing.T) {
	h := setupTestHandler()
	
	// Create one
	_, _ = h.orchestrator.CreateServer(context.Background(), "skywars", 8)

	req := httptest.NewRequest(http.MethodGet, "/servers", nil)
	w := httptest.NewRecorder()

	h.ListServers(w, req)

	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %d", res.StatusCode)
	}

	var body []ServerResponse
	err := json.NewDecoder(res.Body).Decode(&body)
	if err != nil {
		t.Fatal(err)
	}

	if len(body) != 1 {
		t.Fatalf("expected 1 server, got %d", len(body))
	}
}

func TestDeleteServer(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		h := setupTestHandler()
		s, _ := h.orchestrator.CreateServer(context.Background(), "skywars", 8)
		// Manually transition to running
		_ = s.Transition(server.EventStart)
		_ = s.Transition(server.EventRun)

		req := httptest.NewRequest(http.MethodDelete, "/servers/"+s.ID, nil)
		
		// Setup Chi context
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", s.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		
		w := httptest.NewRecorder()

		h.DeleteServer(w, req)

		if w.Code != http.StatusAccepted {
			t.Errorf("expected status 202, got %d", w.Code)
		}

		if s.State() != server.StateDraining {
			t.Errorf("expected state draining, got %s", s.State())
		}
	})

	t.Run("Conflict_NotRunning", func(t *testing.T) {
		h := setupTestHandler()
		s, _ := h.orchestrator.CreateServer(context.Background(), "skywars", 8)
		// State is Pending

		req := httptest.NewRequest(http.MethodDelete, "/servers/"+s.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", s.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		
		w := httptest.NewRecorder()

		h.DeleteServer(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("expected status 409, got %d", w.Code)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		h := setupTestHandler()
		req := httptest.NewRequest(http.MethodDelete, "/servers/nonexistent", nil)
		
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		h.DeleteServer(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", w.Code)
		}
	})
}
