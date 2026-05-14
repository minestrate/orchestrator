package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/moby/moby/api/types/network"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/internal/api"
	"github.com/mitsuakki/minestrate/internal/auth"
	"github.com/mitsuakki/minestrate/internal/middleware"
	"github.com/mitsuakki/minestrate/internal/server"
)

type mockDockerClient struct{}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateRequest) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}

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
		log.Fatalf("failed to load config: %v", err)
	}

	if cfg.Env == "dev" {
		claims := &auth.Claims{
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
			log.Printf("failed to generate dev token: %v", err)
		} else {
			fmt.Printf("Dev JWT: %s\n", ss)
		}
	}

	r := chi.NewRouter()

	orchestrator, err := server.NewOrchestrator(cfg, &mockDockerClient{})
	if err != nil {
		log.Fatalf("failed to create orchestrator: %v", err)
	}
	orchestrator.StartWorkers()
	h := api.NewHandler(orchestrator)

	// ToDo : Public routes

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(cfg.Auth.JWTSecret))

		r.Get("/servers", h.ListServers)
		r.Get("/servers/{id}", h.GetServer)
		r.Delete("/servers/{id}", h.DeleteServer)
		r.With(middleware.RequireScope("server:create")).Post("/servers", h.CreateServer)
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Printf("Starting server on %s\n", addr)

	if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
		log.Fatal(http.ListenAndServeTLS(addr, cfg.Server.TLSCert, cfg.Server.TLSKey, r))
	}

	log.Fatal(http.ListenAndServe(addr, r))
}
