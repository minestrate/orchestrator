package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mitsuakki/minestrate/internal/server"
)

type Handler struct {
	orchestrator *server.Orchestrator
}

func NewHandler(o *server.Orchestrator) *Handler {
	return &Handler{orchestrator: o}
}

func (h *Handler) CreateServer(w http.ResponseWriter, r *http.Request) {
	var req CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Game) == "" {
		http.Error(w, "game is required", http.StatusBadRequest)
		return
	}

	if req.Players < 1 || req.Players > 100 {
		http.Error(w, "players must be between 1 and 100", http.StatusBadRequest)
		return
	}

	s, err := h.orchestrator.CreateServer(r.Context(), req.Game, req.Players)
	if err != nil {
		if errors.Is(err, server.ErrMaxServersReached) ||
			errors.Is(err, server.ErrNoPortsAvailable) ||
			errors.Is(err, server.ErrJobQueueFull) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(ToServerResponse(s))
}

func (h *Handler) ListServers(w http.ResponseWriter, r *http.Request) {
	servers := h.orchestrator.ListServers()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ToServerListResponse(servers))
}

func (h *Handler) GetServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, ok := h.orchestrator.GetServer(id)
	if !ok {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ToServerResponse(s))
}

func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.orchestrator.ShutdownServer(r.Context(), id); err != nil {
		if errors.Is(err, server.ErrServerNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, server.ErrServerNotRunning) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

