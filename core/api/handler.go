package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	orchestrator "github.com/mitsuakki/minestrate/core"
	"github.com/mitsuakki/minestrate/core/domain"
)

type Handler struct {
	orchestrator *orchestrator.Orchestrator
}

var metricsHandler = promhttp.Handler()

func NewHandler(o *orchestrator.Orchestrator) *Handler {
	return &Handler{orchestrator: o}
}

// MetricsHandler exposes Prometheus metrics. No auth — intended for internal scraping.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	metricsHandler.ServeHTTP(w, r)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	uptime, active, free, full := h.orchestrator.Metrics()
	dockerOK := h.orchestrator.DockerReachable(r.Context())

	status := http.StatusOK
	if full || !dockerOK {
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":          "ok",
		"uptime_seconds":  int64(uptime),
		"servers_active":  active,
		"port_pool_free":  free,
		"docker_reachable": dockerOK,
	})
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

	s, err := h.orchestrator.CreateServer(r.Context(), orchestrator.CreateServerOptions{
		Game:        req.Game,
		Players:     req.Players,
		NetworkName: req.NetworkName,
		TTLSeconds:  req.TTLSeconds,
		WebhookURL:  req.WebhookURL,
		Labels:      req.Labels,
	})
	if err != nil {
		if errors.Is(err, orchestrator.ErrMaxServersReached) ||
			errors.Is(err, orchestrator.ErrNoPortsAvailable) ||
			errors.Is(err, orchestrator.ErrJobQueueFull) {
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
	labelFilters := parseLabelFilters(r)
	limit, offset := parsePagination(r)
	servers, total := h.orchestrator.ListServersByLabels(labelFilters, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ServerListResponse{
		Servers: ToServerListResponse(servers),
		Total:   total,
		Limit:   limit,
		Offset:  offset,
	})
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 50 // default
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

// parseLabelFilters extracts label=key:value query params and returns them as a map.
func parseLabelFilters(r *http.Request) map[string]string {
	values := r.URL.Query()["label"]
	if len(values) == 0 {
		return nil
	}
	filters := make(map[string]string, len(values))
	for _, v := range values {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 {
			filters[parts[0]] = parts[1]
		}
	}
	if len(filters) == 0 {
		return nil
	}
	return filters
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
		if errors.Is(err, orchestrator.ErrServerNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, orchestrator.ErrServerNotRunning) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		var invalidTransition *domain.ErrInvalidTransition
		if errors.As(err, &invalidTransition) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) GetServerHealth(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	health, err := h.orchestrator.ServerHealth(r.Context(), id)
	if err != nil {
		if errors.Is(err, orchestrator.ErrServerNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(health)
}

func (h *Handler) RecordHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.orchestrator.RecordHeartbeat(id); err != nil {
		if errors.Is(err, orchestrator.ErrServerNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ExtendServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.orchestrator.ExtendServerTTL(id); err != nil {
		if errors.Is(err, orchestrator.ErrServerNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if errors.Is(err, orchestrator.ErrServerNoTTL) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AdminBackup streams a consistent snapshot of the database.
func (h *Handler) AdminBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=minestrate-%s.db", time.Now().UTC().Format("20060102-150405")))
	if err := h.orchestrator.BackupStore(w); err != nil {
		http.Error(w, fmt.Sprintf("backup failed: %v", err), http.StatusInternalServerError)
		return
	}
}

// AdminRestore restores the database from an uploaded backup.
func (h *Handler) AdminRestore(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if err := h.orchestrator.RestoreStore(r.Body); err != nil {
		http.Error(w, fmt.Sprintf("restore failed: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "restored"})
}
