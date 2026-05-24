package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/mitsuakki/minestrate/api/service"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/domain"
	"github.com/mitsuakki/minestrate/orchestrator"
)

func setupTestHandler() *Handler {
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 10
	cfg.Orchestrator.StartTimeout = 30
	cfg.Ports.RangeStart = 19132
	cfg.Ports.RangeEnd = 19142
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"

	o, _ := orchestrator.NewOrchestrator(cfg, &orchestrator.MockDockerClient{})
	rm := service.NewRefreshManager("this-is-a-very-long-secret-key-32-bytes")
	return NewHandler(o, rm)
}

func TestCreateServer(t *testing.T) {
	h := setupTestHandler()

	t.Run("ValidRequest", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "skywars", Players: 8}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		if w.Code != http.StatusAccepted {
			t.Errorf("Expected status 202, got %d", w.Code)
		}

		var resp ServerResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.Game != "skywars" {
			t.Errorf("Expected game skywars, got %s", resp.Game)
		}
	})

	t.Run("InvalidGame", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "", Players: 8}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

func TestListServers(t *testing.T) {
	h := setupTestHandler()

	// Create a server first
	reqBody := CreateServerRequest{Game: "survival", Players: 20}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	h.CreateServer(w, req)

	// Create another server and stop it
	reqBody2 := CreateServerRequest{Game: "creative", Players: 10}
	body2, _ := json.Marshal(reqBody2)
	w2 := httptest.NewRecorder()
	h.CreateServer(w2, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body2)))
	var created2 ServerResponse
	if err := json.NewDecoder(w2.Body).Decode(&created2); err != nil {
		t.Fatal(err)
	}
	if err := h.orchestrator.StopServer(context.Background(), created2.ID); err != nil {
		t.Fatal(err)
	}

	// List servers
	req = httptest.NewRequest(http.MethodGet, "/servers", nil)
	w = httptest.NewRecorder()
	h.ListServers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp []ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// Should only have 1 server (the non-stopped one)
	if len(resp) != 1 {
		t.Errorf("Expected 1 server, got %d", len(resp))
	}
	if resp[0].Created.IsZero() {
		t.Error("Expected Created field to be set")
	}
}

func TestGetServer(t *testing.T) {
	h := setupTestHandler()

	// Create a server
	reqBody := CreateServerRequest{Game: "bedwars", Players: 4}
	body, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

	var created ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	t.Run("Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/servers/"+created.ID, nil)
		// Manually set URL param because we're calling handler directly
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.GetServer(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/servers/non-existent", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "non-existent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.GetServer(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

func TestHealthCheck(t *testing.T) {
	h := setupTestHandler()

	t.Run("StatusOK", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		h.HealthCheck(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp["status"] != "ok" {
			t.Errorf("Expected status ok, got %v", resp["status"])
		}
		if _, ok := resp["uptime_seconds"]; !ok {
			t.Error("Missing uptime_seconds")
		}
		if _, ok := resp["servers_active"]; !ok {
			t.Error("Missing servers_active")
		}
		if _, ok := resp["port_pool_free"]; !ok {
			t.Error("Missing port_pool_free")
		}
	})
}

func TestDeleteServer(t *testing.T) {
	h := setupTestHandler()

	// Create a server
	reqBody := CreateServerRequest{Game: "test", Players: 10}
	body, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

	var created ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// In MockDockerClient, it's not actually running unless we process jobs.
	// But ShutdownServer only checks if state is Running.
	// Since we use NewOrchestrator with MockDockerClient, it might not be Running immediately.
	// We need to transition it to Running for ShutdownServer to work (or it returns ErrServerNotRunning).

	s, _ := h.orchestrator.GetServer(created.ID)
	if err := s.Transition(domain.EventStart); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(domain.EventRun); err != nil {
		t.Fatal(err)
	}

	t.Run("Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/servers/"+created.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.DeleteServer(w, req)

		if w.Code != http.StatusAccepted {
			t.Errorf("Expected status 202, got %d", w.Code)
		}
	})

	t.Run("NotRunning", func(t *testing.T) {
		// Create another one and leave it in Pending
		w = httptest.NewRecorder()
		h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))
		var pending ServerResponse
		if err := json.NewDecoder(w.Body).Decode(&pending); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodDelete, "/servers/"+pending.ID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", pending.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.DeleteServer(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("Expected status 409, got %d", w.Code)
		}
	})
}
