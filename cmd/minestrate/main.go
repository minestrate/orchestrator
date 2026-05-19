package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/docker/docker/client"
	apihttp "github.com/mitsuakki/minestrate/api/http"
	"github.com/mitsuakki/minestrate/api/middleware"
	"github.com/mitsuakki/minestrate/api/service"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/logger"
	"github.com/mitsuakki/minestrate/network"
	"github.com/mitsuakki/minestrate/orchestrator"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	version := flag.Bool("version", false, "print version")
	flag.Parse()

	if len(os.Args) < 2 {
		fmt.Println("Isolated Minecraft minigame servers, on demand. REST API over Docker, written in Go.")
		return
	}

	if *version {
		fmt.Println("Version: dev")
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
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
	
	refreshManager := service.NewRefreshManager(cfg.Auth.JWTSecret)
	h := apihttp.NewHandler(o, refreshManager)

	// ToDo : Public routes

	r.Group(func(r chi.Router) {
		r.Post("/auth/refresh", h.RefreshToken)
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(cfg.Auth.JWTSecret))

		r.Get("/servers", h.ListServers)
		r.Get("/servers/{id}", h.GetServer)
		r.Delete("/servers/{id}", h.DeleteServer)
		r.With(middleware.RequireScope("server:create")).Post("/servers", h.CreateServer)
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("Starting server", "addr", addr)

	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		if err := http.ListenAndServeTLS(addr, cfg.Server.TLSCert, cfg.Server.TLSKey, r); err != nil {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}

	if err := http.ListenAndServe(addr, r); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}
