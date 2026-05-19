package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/mitsuakki/minestrate/config"
	"github.com/mitsuakki/minestrate/domain"
	"github.com/mitsuakki/minestrate/network"
	"github.com/docker/go-connections/nat"
)

type Orchestrator struct {
	cfg          *config.Config
	servers      map[string]*domain.Server
	serversMutex sync.RWMutex
	ports        *network.PortAllocator
	networks     network.NetworkManager
	docker       network.DockerClient
	jobQueue     chan *domain.Server
}

func NewOrchestrator(cfg *config.Config, docker network.DockerClient) (*Orchestrator, error) {
	var nm network.NetworkManager
	var err error

	mode := cfg.Network.Mode
	if mode == "" {
		mode = "simple"
	}

	switch mode {
	case "simple":
		if mode == "simple" {
			if err := network.EnsureNetwork(context.Background(), docker, cfg.Network.DefaultNetwork); err != nil {
				return nil, fmt.Errorf("failed to ensure network %q: %w", cfg.Network.DefaultNetwork, err)
			}
		}
		
		nm = network.NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
	case "isolated":
		nm, err = network.NewIsolatedSubnetManager(docker, cfg.Network.SubnetBlock)
		if err != nil {
			return nil, err
		}
	default:
		return nil, network.ErrInvalidNetworkMode
	}

	if cfg.Network.EnableFallback && mode == "isolated" {
		secondary := network.NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
		nm = network.NewFallbackNetworkManager(nm, secondary)
	}

	o := &Orchestrator{
		cfg:      cfg,
		servers:  make(map[string]*domain.Server),
		ports:    network.NewPortAllocator(cfg.Ports.RangeStart, cfg.Ports.RangeEnd),
		networks: nm,
		docker:   docker,
		jobQueue: make(chan *domain.Server, cfg.Orchestrator.Workers),
	}

	return o, nil
}

func (o *Orchestrator) CreateServer(ctx context.Context, game string, players int) (*domain.Server, error) {
	o.serversMutex.Lock()
	if len(o.servers) >= o.cfg.Orchestrator.MaxServers {
		o.serversMutex.Unlock()
		return nil, domain.ErrMaxServersReached
	}

	port, err := o.ports.Acquire()
	if err != nil {
		o.serversMutex.Unlock()
		return nil, err
	}

	id := uuid.New().String()

	netCfg, err := o.networks.Allocate(ctx, id)
	if err != nil {
		o.ports.Release(port)
		o.serversMutex.Unlock()
		return nil, err
	}

	s := domain.NewServer(id, game, players, "127.0.0.1", port)
	s.Network = map[string]interface{}{
		"network_name": netCfg.NetworkName,
		"subnet":       netCfg.Subnet,
		"gateway":      netCfg.Gateway,
	}

	o.servers[id] = s
	o.serversMutex.Unlock()

	cleanup := func() {
		o.serversMutex.Lock()
		delete(o.servers, id)
		o.serversMutex.Unlock()
		o.ports.Release(port)
		_ = o.networks.Release(ctx, id)
	}

	select {
	case o.jobQueue <- s:
		return s, nil
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	default:
		cleanup()
		return nil, domain.ErrJobQueueFull
	}
}

func (o *Orchestrator) StopServer(ctx context.Context, id string) error {
	o.serversMutex.Lock()
	defer o.serversMutex.Unlock()

	s, ok := o.servers[id]
	if !ok {
		return domain.ErrServerNotFound
	}

	if err := s.Transition(domain.EventStop); err != nil {
		return err
	}

	delete(o.servers, id)
	o.ports.Release(s.Port)
	return o.networks.Release(ctx, id)
}

func (o *Orchestrator) ShutdownServer(ctx context.Context, id string) error {
	o.serversMutex.RLock()
	s, ok := o.servers[id]
	o.serversMutex.RUnlock()

	if !ok {
		return domain.ErrServerNotFound
	}

	if s.State() != domain.StateRunning {
		return domain.ErrServerNotRunning
	}

	if err := s.Transition(domain.EventDrain); err != nil {
		return err
	}

	go func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		containerName := fmt.Sprintf("minestrate-%s-%s", s.Game, s.ID[:8])
		_ = o.docker.ContainerStop(cleanupCtx, containerName, container.StopOptions{})
		
		o.ports.Release(s.Port)
		_ = o.networks.Release(cleanupCtx, s.ID)
		
		_ = s.Transition(domain.EventStop)
	}()

	return nil
}

func (o *Orchestrator) GC() {
	o.serversMutex.Lock()
	defer o.serversMutex.Unlock()

	for id, s := range o.servers {
		if s.State() == domain.StateStopped {
			delete(o.servers, id)
		}
	}
}

func (o *Orchestrator) StartGC(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			o.GC()
		}
	}()
}

func (o *Orchestrator) GetServer(id string) (*domain.Server, bool) {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()
	s, ok := o.servers[id]
	return s, ok
}

func (o *Orchestrator) ListServers() []*domain.Server {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()
	list := make([]*domain.Server, 0, len(o.servers))
	for _, s := range o.servers {
		list = append(list, s)
	}
	return list
}

func (o *Orchestrator) StartWorkers() {
	for i := 0; i < o.cfg.Orchestrator.Workers; i++ {
		go o.worker(i)
	}
}

func (o *Orchestrator) worker(id int) {
	for s := range o.jobQueue {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(o.cfg.Orchestrator.StartTimeout)*time.Second)
		
		err := o.processJob(ctx, s)
		cancel()

		if err != nil {
			_ = s.Transition(domain.EventStop)
			o.serversMutex.Lock()
			delete(o.servers, s.ID)
			o.ports.Release(s.Port)
			_ = o.networks.Release(context.Background(), s.ID)
			o.serversMutex.Unlock()
		}
	}
}

func (o *Orchestrator) processJob(ctx context.Context, s *domain.Server) error {
	if err := s.Transition(domain.EventStart); err != nil {
		return err
	}

	containerName := fmt.Sprintf("minestrate-%s-%s", s.Game, s.ID[:8])
	resp, err := o.docker.ContainerCreate(ctx, &container.Config{
		Image: o.cfg.Docker.Image,
		Labels: map[string]string{
			"minestrate.server_id": s.ID,
		},
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(s.Network["network_name"].(string)),
		PortBindings: nat.PortMap{
			nat.Port("19132/udp"): []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: fmt.Sprintf("%d", s.Port),
				},
			},
		},
	}, nil, nil, containerName)

	if err != nil {
		return err
	}

	if err := o.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return err
	}

	return s.Transition(domain.EventRun)
}
