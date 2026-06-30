package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	docker_network "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
	"github.com/mitsuakki/minestrate/orchestrator/domain"
	allocator2 "github.com/mitsuakki/minestrate/orchestrator/internal/allocator"
)

type Orchestrator struct {
	cfg          *Config
	startTime    time.Time
	servers      map[string]*domain.Server
	serversMutex sync.RWMutex
	ports        *allocator2.PortAllocator
	networks     allocator2.NetworkManager
	docker       DockerClient
	jobQueue     chan *domain.Server
}

func NewOrchestrator(cfg *Config, docker DockerClient) (*Orchestrator, error) {
	var nm allocator2.NetworkManager
	var err error

	mode := cfg.Network.Mode
	if mode == "" {
		mode = "simple"
	}

	switch mode {
	case "simple":
		if err := allocator2.EnsureNetwork(context.Background(), docker, cfg.Network.DefaultNetwork); err != nil {
			return nil, fmt.Errorf("failed to ensure network %q: %w", cfg.Network.DefaultNetwork, err)
		}
		nm = allocator2.NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
	case "isolated":
		nm, err = allocator2.NewIsolatedSubnetManager(docker, cfg.Network.SubnetBlock)
		if err != nil {
			return nil, err
		}
	default:
		return nil, allocator2.ErrInvalidNetworkMode
	}

	if cfg.Network.EnableFallback && mode == "isolated" {
		secondary := allocator2.NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
		nm = allocator2.NewFallbackNetworkManager(nm, secondary)
	}

	o := &Orchestrator{
		cfg:       cfg,
		startTime: time.Now(),
		servers:   make(map[string]*domain.Server),
		ports:     allocator2.NewPortAllocator(cfg.Ports.RangeStart, cfg.Ports.RangeEnd),
		networks:  nm,
		docker:    docker,
		jobQueue:  make(chan *domain.Server, cfg.Orchestrator.Workers),
	}

	return o, nil
}

func (o *Orchestrator) CreateNetwork(ctx context.Context, name string, subnet string) error {
	opts := docker_network.CreateOptions{
		Driver: "bridge",
	}
	if subnet != "" {
		opts.IPAM = &docker_network.IPAM{
			Config: []docker_network.IPAMConfig{
				{
					Subnet: subnet,
				},
			},
		}
	}
	_, err := o.docker.NetworkCreate(ctx, name, opts)
	return err
}

func (o *Orchestrator) CreateServer(ctx context.Context, game string, players int, networkName string) (*domain.Server, error) {
	o.serversMutex.Lock()
	if len(o.servers) >= o.cfg.Orchestrator.MaxServers {
		o.serversMutex.Unlock()
		return nil, ErrMaxServersReached
	}

	port, err := o.ports.Acquire()
	if err != nil {
		o.serversMutex.Unlock()
		return nil, err
	}

	id := uuid.New().String()

	var netName string
	var subnet string
	var gateway string
	var isDynamic bool

	if networkName != "" {
		// Use user-provided network (non-dynamic, meaning it won't be deleted on container stop)
		netName = networkName
		isDynamic = false
	} else {
		// Allocate a dynamic isolated network subnet
		netCfg, err := o.networks.Allocate(ctx, id)
		if err != nil {
			o.ports.Release(port)
			o.serversMutex.Unlock()
			return nil, err
		}
		netName = netCfg.NetworkName
		subnet = netCfg.Subnet
		gateway = netCfg.Gateway
		isDynamic = true
	}

	s := domain.NewServer(id, game, players, "127.0.0.1", port)
	s.Network = domain.NetworkInfo{
		NetworkName: netName,
		Subnet:      subnet,
		Gateway:     gateway,
		IsDynamic:   isDynamic,
	}

	o.servers[id] = s
	o.serversMutex.Unlock()

	cleanup := func() {
		o.serversMutex.Lock()
		delete(o.servers, id)
		o.serversMutex.Unlock()
		o.ports.Release(port)
		if isDynamic {
			_ = o.networks.Release(ctx, id)
		}
	}

	select {
	case o.jobQueue <- s:
		return s, nil
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	default:
		cleanup()
		return nil, ErrJobQueueFull
	}
}

func (o *Orchestrator) StopServer(ctx context.Context, id string) error {
	o.serversMutex.Lock()
	defer o.serversMutex.Unlock()

	s, ok := o.servers[id]
	if !ok {
		return ErrServerNotFound
	}

	if err := s.Transition(domain.EventStop); err != nil {
		return err
	}

	containerName := fmt.Sprintf("tokens-%s-%s", s.Game, s.ID[:8])
	_ = o.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	delete(o.servers, id)
	o.ports.Release(s.Port)
	if s.Network.IsDynamic {
		return o.networks.Release(ctx, id)
	}
	return nil
}

func (o *Orchestrator) ShutdownServer(ctx context.Context, id string) error {
	o.serversMutex.RLock()
	s, ok := o.servers[id]
	o.serversMutex.RUnlock()

	if !ok {
		return ErrServerNotFound
	}

	if s.State() != domain.StateRunning {
		return ErrServerNotRunning
	}

	if err := s.Transition(domain.EventDrain); err != nil {
		return err
	}

	go func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		containerName := fmt.Sprintf("tokens-%s-%s", s.Game, s.ID[:8])
		_ = o.docker.ContainerStop(cleanupCtx, containerName, container.StopOptions{})
		_ = o.docker.ContainerRemove(cleanupCtx, containerName, container.RemoveOptions{Force: true})

		o.ports.Release(s.Port)
		if s.Network.IsDynamic {
			_ = o.networks.Release(cleanupCtx, s.ID)
		}

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

func (o *Orchestrator) ShutdownAll(ctx context.Context) {
	o.serversMutex.RLock()
	activeServers := make([]*domain.Server, 0)
	for _, s := range o.servers {
		if s.State() != domain.StateStopped {
			activeServers = append(activeServers, s)
		}
	}
	o.serversMutex.RUnlock()

	var wg sync.WaitGroup
	for _, s := range activeServers {
		wg.Add(1)
		go func(srv *domain.Server) {
			defer wg.Done()

			_ = srv.Transition(domain.EventDrain)

			containerName := fmt.Sprintf("tokens-%s-%s", srv.Game, srv.ID[:8])
			fmt.Printf("Stopping container: %s\n", containerName)

			_ = o.docker.ContainerStop(ctx, containerName, container.StopOptions{})
			_ = o.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

			o.ports.Release(srv.Port)
			if srv.Network.IsDynamic {
				_ = o.networks.Release(ctx, srv.ID)
			}

			_ = srv.Transition(domain.EventStop)
		}(s)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("All servers shut down gracefully.")
	case <-ctx.Done():
		fmt.Println("Shutdown timed out, some containers may still be running.")
	}
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
		if s.State() != domain.StateStopped {
			list = append(list, s)
		}
	}
	return list
}

func (o *Orchestrator) Metrics() (uptime float64, activeServers int, freePorts int, jobQueueFull bool) {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()

	uptime = time.Since(o.startTime).Seconds()
	activeServers = 0
	for _, s := range o.servers {
		if s.State() != domain.StateStopped {
			activeServers++
		}
	}
	freePorts = o.ports.FreePorts()
	jobQueueFull = len(o.jobQueue) == cap(o.jobQueue)
	return
}

func (o *Orchestrator) StartWorkers() {
	for i := 0; i < o.cfg.Orchestrator.Workers; i++ {
		go o.worker(i)
	}
}

func (o *Orchestrator) worker(_ int) {
	for s := range o.jobQueue {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(o.cfg.Orchestrator.StartTimeout)*time.Second)

		err := o.processJob(ctx, s)
		cancel()

		if err != nil {
			_ = s.Transition(domain.EventStop)
			containerName := fmt.Sprintf("tokens-%s-%s", s.Game, s.ID[:8])
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
			_ = o.docker.ContainerRemove(cleanupCtx, containerName, container.RemoveOptions{Force: true})

			o.serversMutex.Lock()
			delete(o.servers, s.ID)
			o.ports.Release(s.Port)
			if s.Network.IsDynamic {
				_ = o.networks.Release(cleanupCtx, s.ID)
			}
			o.serversMutex.Unlock()
			cancelCleanup()
		}
	}
}

func (o *Orchestrator) processJob(ctx context.Context, s *domain.Server) error {
	if err := s.Transition(domain.EventStart); err != nil {
		return err
	}

	containerName := fmt.Sprintf("tokens-%s-%s", s.Game, s.ID[:8])
	resp, err := o.docker.ContainerCreate(ctx, &container.Config{
		Image: o.cfg.Docker.Image,
		Labels: map[string]string{
			"tokens.server_id": s.ID,
		},
	}, &container.HostConfig{
		NetworkMode: container.NetworkMode(s.Network.NetworkName),
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
