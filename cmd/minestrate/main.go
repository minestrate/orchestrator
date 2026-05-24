package main

import (
	"context"
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
	apihttp "github.com/mitsuakki/minestrate/api/http"
	"github.com/mitsuakki/minestrate/api/middleware"
	"github.com/mitsuakki/minestrate/api/service"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/logger"
	"github.com/mitsuakki/minestrate/network"
	"github.com/mitsuakki/minestrate/orchestrator"
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CONFIGURATION ERROR: %v\n", err)
		os.Exit(1)
	}
	logger.Init(cfg.Env)

	if cfg.Env == "dev" {
		claims := &service.Claims{
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
	}

	r := chi.NewRouter()

	var dockerClient network.DockerClient
	if cfg.Env == "dev" && cfg.Docker.Socket == "" {
		dockerClient = &network.MockDockerClient{}
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

	rateLimiter := middleware.NewRateLimiter(context.Background(), cfg.Auth.RateLimit.RefillRate, cfg.Auth.RateLimit.Capacity)

	refreshManager := service.NewRefreshManager(cfg.Auth.JWTSecret)
	h := apihttp.NewHandler(o, refreshManager)

	r.Get("/health", h.HealthCheck)

	r.Group(func(r chi.Router) {
		r.Post("/auth/refresh", h.RefreshToken)
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(cfg.Auth.JWTSecret))
		r.Use(rateLimiter.Middleware)

		r.Get("/servers", h.ListServers)
		r.Get("/servers/{id}", h.GetServer)
		r.Delete("/servers/{id}", h.DeleteServer)
		r.With(middleware.RequireScope("server:create")).Post("/servers", h.CreateServer)
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Signal handling for graceful shutdown
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
		if err != nil && err != http.ErrServerClosed {
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
