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
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	orchestrator "github.com/mitsuakki/minestrate/core"
	"github.com/mitsuakki/minestrate/core/api"
	"github.com/mitsuakki/minestrate/core/dockerclient"
)

func main() {
	configPath := flag.String("config", "minestrate.yaml", "path to config file")
	version := flag.Bool("version", false, "print version")
	flag.Parse()

	if len(os.Args) < 2 && *configPath == "minestrate.yaml" {
		if _, err := os.Stat(*configPath); os.IsNotExist(err) {
			fmt.Println("Isolated Minecraft minigame servers, on demand. REST API over Docker, written in Go.")
			fmt.Printf("Default config 'minestrate.yaml' not found. Use --config to specify a path.\n")
			return
		}
	}

	if *version {
		fmt.Println("Version: dev")
		return
	}

	cfg, err := orchestrator.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONFIGURATION ERROR: %v\n", err)
		os.Exit(1)
	}

	var handler slog.Handler
	if cfg.Env == "prod" {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})
	}
	slog.SetDefault(slog.New(handler))

	if cfg.Env == "dev" {
		claims := &api.Claims{
			Scope: []string{"server:create"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				NotBefore: jwt.NewNumericDate(time.Now()),
				Issuer:    "minestrate-dev",
				Subject:   "admin",
			},
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		ss, err := token.SignedString([]byte(cfg.Auth.JWTSecret))
		if err != nil {
			slog.Error("failed to generate dev token", "error", err)
		} else {
			fmt.Printf("Dev JWT: %s\n", ss)
		}
	} else if cfg.Env == "prod" {
		if cfg.Auth.JWTSecret == "this-is-a-very-long-secret-key-32-bytes" {
			slog.Error("insecure default JWT secret detected in production")
			os.Exit(1)
		}
	}

	r := chi.NewRouter()

	var dockerClient dockerclient.Client
	if cfg.Env == "dev" && cfg.Docker.Socket == "" {
		dockerClient = &dockerclient.MockClient{}
	} else {
		opts := []client.Opt{client.WithAPIVersionNegotiation()}
		if cfg.Docker.Socket != "" {
			opts = append(opts, client.WithHost(cfg.Docker.Socket))
		}

		var err error
		dockerClient, err = client.NewClientWithOpts(opts...)
		if err != nil {
			slog.Error("failed to create docker client", "error", err)
			os.Exit(1)
		}
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
		r.With(api.RequireScope("server:create")).Post("/networks", h.CreateNetwork)
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
