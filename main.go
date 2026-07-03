package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/go-chi/chi/v5"
	orchestrator "github.com/mitsuakki/minestrate/core"
	"github.com/mitsuakki/minestrate/core/api"
)

func main() {
	configPath := flag.String("config", "minestrate.yaml", "path to config file")
	version := flag.Bool("version", false, "print version")
	flag.Parse()

	if len(os.Args) < 2 && *configPath == "minestrate.yaml" {
		if _, err := os.Stat(*configPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Isolated Minecraft minigame servers, on demand. REST API over Docker, written in Go.\n")
			fmt.Fprintf(os.Stderr, "Default config 'minestrate.yaml' not found. Use --config to specify a path.\n")
			return
		}
	}

	if *version {
		fmt.Fprintf(os.Stdout, "Version: 1\n")
		return
	}

	cfg, err := orchestrator.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONFIGURATION ERROR: %v\n", err)
		os.Exit(1)
	}

	logLevel := slogLevelFromEnv()
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))

	if cfg.Auth.JWTSecret == "this-is-a-very-long-secret-key-32-bytes" {
		slog.Warn("using default JWT secret — generate a strong one: go run ./cmd/jwtgen/ --secret <your-secret>")
	}

	r := chi.NewRouter()
	r.Use(requestLogger)

	opts := []client.Opt{client.WithAPIVersionNegotiation()}
	if cfg.Docker.Socket != "" {
		opts = append(opts, client.WithHost(cfg.Docker.Socket))
	}
	dockerClient, err := client.NewClientWithOpts(opts...)
	if err != nil {
		slog.Error("failed to create docker client", "error", err)
		os.Exit(1)
	}

	o, err := orchestrator.NewOrchestrator(cfg, dockerClient)
	if err != nil {
		slog.Error("failed to create orchestrator", "error", err)
		os.Exit(1)
	}
	o.StartWorkers()
	o.StartGC(1 * time.Minute)

	rateLimiter := api.NewRateLimiter(context.Background(), cfg.Auth.RateLimit.RefillRate, cfg.Auth.RateLimit.Capacity)
	h := api.NewHandler(o)

	r.Get("/health", h.HealthCheck)
	r.Get("/metrics", api.MetricsHandler)

	// Redirect legacy paths to /v1 equivalents.
	r.Get("/servers", redirectV1)
	r.Get("/servers/*", redirectV1)
	r.Post("/servers", redirectV1)
	r.Post("/servers/*", redirectV1)
	r.Delete("/servers/*", redirectV1)
	r.Route("/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(api.Auth(cfg.Auth.JWTSecret))
			r.Use(rateLimiter.Middleware)

			r.Get("/servers", h.ListServers)
			r.Get("/servers/{id}", h.GetServer)
			r.Get("/servers/{id}/health", h.GetServerHealth)
			r.Post("/servers/{id}/heartbeat", h.RecordHeartbeat)
			r.Post("/servers/{id}/extend", h.ExtendServer)
			r.Delete("/servers/{id}", h.DeleteServer)
			r.With(api.RequireScope("server:create")).Post("/servers", h.CreateServer)

			// Admin endpoints.
			r.With(api.RequireScope("admin")).Get("/admin/backup", h.AdminBackup)
			r.With(api.RequireScope("admin")).Post("/admin/restore", h.AdminRestore)
		})
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("Starting server", "addr", addr)
		var err error
		if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
			err = server.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("Shutting down gracefully...")

	rateLimiter.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown failed", "error", err)
	}

	o.ShutdownAll(shutdownCtx)
	slog.Info("Exit.")
}

// redirectV1 redirects legacy paths to their /v1 equivalents.
func redirectV1(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/v1"+r.URL.Path, http.StatusMovedPermanently)
}

// slogLevelFromEnv reads LOG_LEVEL and returns the corresponding slog.Level.
func slogLevelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// requestLogger is a chi middleware that logs every HTTP request.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		duration := time.Since(start).Microseconds()

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration_ms", float64(duration)/1000.0,
			"player_sub", playerSubject(r),
		)
	})
}

// playerSubject extracts the JWT subject from the request context.
func playerSubject(r *http.Request) string {
	claims, ok := r.Context().Value(api.ClaimsKey).(*api.Claims)
	if !ok || claims == nil {
		return ""
	}
	return claims.Subject
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
