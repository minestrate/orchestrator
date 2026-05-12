package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/internal/server"
)

func setupTestHandler() *Handler {
	cfg := &config.Config{}
	cfg.Orchestrator.MaxServers = 10
	cfg.Orchestrator.Workers = 1
	cfg.Ports.RangeStart = 19132
	cfg.Ports.RangeEnd = 19142

	o := server.NewOrchestrator(cfg)
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
	_, _ = h.orchestrator.CreateServer("skywars", 8)

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
