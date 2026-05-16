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
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/internal/api"
	"github.com/mitsuakki/minestrate/internal/auth"
	"github.com/mitsuakki/minestrate/internal/middleware"
	"github.com/mitsuakki/minestrate/internal/server"
)

type mockDockerClient struct{}

func (m *mockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *mockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}
func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	return container.CreateResponse{ID: containerName}, nil
}
func (m *mockDockerClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	return nil
}
func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
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

	var dockerClient server.DockerClient
	if cfg.Env == "dev" && cfg.Docker.Socket == "" {
		dockerClient = &mockDockerClient{}
	} else {
		var err error
		dockerClient, err = client.NewClientWithOpts(
			client.WithHost(cfg.Docker.Socket),
			client.WithAPIVersionNegotiation(),
		)
		if err != nil {
			log.Fatalf("failed to create docker client: %v", err)
		}
	}

	orchestrator, err := server.NewOrchestrator(cfg, dockerClient)
	if err != nil {
		log.Fatalf("failed to create orchestrator: %v", err)
	}
	orchestrator.StartWorkers()
	orchestrator.StartGC(1 * time.Minute)
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
