package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	orchestrator "github.com/mitsuakki/minestrate/core"
	"github.com/mitsuakki/minestrate/core/domain"
)

func setupTestHandler() *Handler {
	cfg := &orchestrator.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 10
	cfg.Orchestrator.StartTimeout = 30
	cfg.Ports.RangeStart = 19132
	cfg.Ports.RangeEnd = 19142
	cfg.Network.Mode = "simple"
	cfg.Network.DefaultNetwork = "test-net"

	o, _ := orchestrator.NewOrchestrator(cfg, &orchestrator.MockDockerClient{})
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

	t.Run("ValidRequestWithWebhook", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "skywars", Players: 8, WebhookURL: "http://example.com/hook"}
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
		// Verify webhook URL was stored.
		s, _ := h.orchestrator.GetServer(resp.ID)
		if s.WebhookURL != "http://example.com/hook" {
			t.Errorf("Expected webhook URL to be stored, got %q", s.WebhookURL)
		}
	})

	t.Run("ValidRequestWithCustomNetwork", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "skywars", Players: 8, NetworkName: "custom-net"}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateServer(w, req)

		if w.Code != http.StatusAccepted {
			t.Errorf("Expected status 202, got %d", w.Code)
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

func TestCreateNetwork(t *testing.T) {
	h := setupTestHandler()

	t.Run("ValidRequest", func(t *testing.T) {
		reqBody := CreateNetworkRequest{Name: "my-custom-net", Subnet: "172.22.0.0/24"}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/networks", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateNetwork(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("Expected status 201, got %d", w.Code)
		}
	})

	t.Run("MissingName", func(t *testing.T) {
		reqBody := CreateNetworkRequest{Name: ""}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/networks", bytes.NewBuffer(body))
		w := httptest.NewRecorder()

		h.CreateNetwork(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", w.Code)
		}
	})
}

func TestListServersWithLabels(t *testing.T) {
	h := setupTestHandler()

	// Create servers with different labels.
	body1, _ := json.Marshal(CreateServerRequest{
		Game: "bedwars", Players: 4,
		Labels: map[string]string{"mode": "bedwars", "region": "eu"},
	})
	w1 := httptest.NewRecorder()
	h.CreateServer(w1, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body1)))

	body2, _ := json.Marshal(CreateServerRequest{
		Game: "skywars", Players: 8,
		Labels: map[string]string{"mode": "skywars", "region": "us"},
	})
	w2 := httptest.NewRecorder()
	h.CreateServer(w2, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body2)))

	// Filter by mode=bedwars.
	req := httptest.NewRequest(http.MethodGet, "/servers?label=mode:bedwars", nil)
	w := httptest.NewRecorder()
	h.ListServers(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	var resp []ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 {
		t.Errorf("Expected 1 server with label mode=bedwars, got %d", len(resp))
	}
	if len(resp) > 0 && resp[0].Game != "bedwars" {
		t.Errorf("Expected bedwars, got %s", resp[0].Game)
	}

	// Filter by region=us.
	req2 := httptest.NewRequest(http.MethodGet, "/servers?label=region:us", nil)
	w = httptest.NewRecorder()
	h.ListServers(w, req2)

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 {
		t.Errorf("Expected 1 server with label region=us, got %d", len(resp))
	}

	// Multi-label filter.
	req3 := httptest.NewRequest(http.MethodGet, "/servers?label=mode:bedwars&label=region:eu", nil)
	w = httptest.NewRecorder()
	h.ListServers(w, req3)

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 {
		t.Errorf("Expected 1 server matching both labels, got %d", len(resp))
	}
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

func TestMetricsEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	MetricsHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Must include our custom metrics.
	for _, name := range []string{
		"minestrate_servers_active",
		"minestrate_pool_utilization",
		"minestrate_spawn_duration_seconds",
		"minestrate_port_pool_free",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("Expected metric %q in response body", name)
		}
	}
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

func TestGetServerHealth(t *testing.T) {
	h := setupTestHandler()

	// Create a server and advance to running
	reqBody := CreateServerRequest{Game: "health-test", Players: 4}
	body, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

	var created ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	s, _ := h.orchestrator.GetServer(created.ID)
	_ = s.Transition(domain.EventStart)
	_ = s.Transition(domain.EventRun)

	t.Run("HealthyServer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/servers/"+created.ID+"/health", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.GetServerHealth(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		var resp orchestrator.ServerHealth
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if !resp.Healthy {
			t.Error("Expected healthy=true for running server with mock Docker")
		}
		if resp.State != string(domain.StateRunning) {
			t.Errorf("Expected state=running, got %s", resp.State)
		}
		if !resp.ContainerRunning {
			t.Error("Expected container_running=true with mock Docker")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/servers/nonexistent/health", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.GetServerHealth(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

func TestRecordHeartbeat(t *testing.T) {
	h := setupTestHandler()

	// Create a server
	reqBody := CreateServerRequest{Game: "hb-test", Players: 2}
	body, _ := json.Marshal(reqBody)
	w := httptest.NewRecorder()
	h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

	var created ServerResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	t.Run("Success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/servers/"+created.ID+"/heartbeat", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.RecordHeartbeat(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("Expected status 204, got %d", w.Code)
		}

		// Verify heartbeat was recorded
		s, _ := h.orchestrator.GetServer(created.ID)
		if s.HeartbeatAge() <= 0 {
			t.Error("Expected heartbeat age > 0 after recording")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/servers/nonexistent/heartbeat", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.RecordHeartbeat(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}

func TestExtendServer(t *testing.T) {
	h := setupTestHandler()

	t.Run("ExtendWithTTL", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "extend-test", Players: 2, TTLSeconds: 60}
		body, _ := json.Marshal(reqBody)
		w := httptest.NewRecorder()
		h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

		var created ServerResponse
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/servers/"+created.ID+"/extend", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.ExtendServer(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("Expected status 204, got %d", w.Code)
		}

		s, _ := h.orchestrator.GetServer(created.ID)
		if s.IsExpired() {
			t.Error("server should not be expired after extend")
		}
	})

	t.Run("ExtendWithoutTTL", func(t *testing.T) {
		reqBody := CreateServerRequest{Game: "no-ttl", Players: 2}
		body, _ := json.Marshal(reqBody)
		w := httptest.NewRecorder()
		h.CreateServer(w, httptest.NewRequest(http.MethodPost, "/servers", bytes.NewBuffer(body)))

		var created ServerResponse
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/servers/"+created.ID+"/extend", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", created.ID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		h.ExtendServer(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status 400 for server without TTL, got %d", w.Code)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/servers/nonexistent/extend", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		h.ExtendServer(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})
}
