package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
	"github.com/google/uuid"
	"github.com/mitsuakki/minestrate/core/dockerclient"
	"github.com/mitsuakki/minestrate/core/domain"
	allocator2 "github.com/mitsuakki/minestrate/core/internal/allocator"
)

type Orchestrator struct {
	cfg          *Config
	startTime    time.Time
	servers      map[string]*domain.Server
	serversMutex sync.RWMutex
	ports        *allocator2.PortAllocator
	networks     allocator2.NetworkManager
	docker       dockerclient.Client
	jobQueue     chan *domain.Server
	ctx          context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
}

func NewOrchestrator(cfg *Config, docker dockerclient.Client) (*Orchestrator, error) {
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

	ctx, cancel := context.WithCancel(context.Background())

	o := &Orchestrator{
		cfg:       cfg,
		startTime: time.Now(),
		servers:   make(map[string]*domain.Server),
		ports:     allocator2.NewPortAllocator(cfg.Ports.RangeStart, cfg.Ports.RangeEnd),
		networks:  nm,
		docker:    docker,
		jobQueue:  make(chan *domain.Server, cfg.Orchestrator.Workers),
		ctx:       ctx,
		cancel:    cancel,
	}

	return o, nil
}

func (o *Orchestrator) CreateNetwork(ctx context.Context, name string, subnet string) error {
	opts := dockernetwork.CreateOptions{
		Driver: "bridge",
	}
	if subnet != "" {
		opts.IPAM = &dockernetwork.IPAM{
			Config: []dockernetwork.IPAMConfig{
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
		return nil, errors.Join(ErrNoPortsAvailable, err)
	}

	id := uuid.New().String()

	var netName string
	var subnet string
	var gateway string
	var isDynamic bool

	if networkName != "" {
		netName = networkName
		isDynamic = false
	} else {
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

	addr := o.cfg.Server.AdvertisedAddress
	if addr == "" {
		addr = "127.0.0.1"
	}
	s := domain.NewServer(id, game, players, addr, port)
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
		if _, exists := o.servers[id]; exists {
			delete(o.servers, id)
			o.serversMutex.Unlock()
			o.ports.Release(port)
			if isDynamic {
				// Use a fresh context so a cancelled request context
				// doesn't abort Docker API calls and leak resources.
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				_ = o.networks.Release(cleanupCtx, id)
			}
		} else {
			o.serversMutex.Unlock()
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
	s, ok := o.servers[id]
	if !ok {
		o.serversMutex.Unlock()
		return ErrServerNotFound
	}

	if err := s.Transition(domain.EventStop); err != nil {
		o.serversMutex.Unlock()
		return err
	}

	delete(o.servers, id)
	port := s.Port
	isDynamic := s.Network.IsDynamic
	o.serversMutex.Unlock()

	containerName := s.ContainerName()
	if err := o.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true}); err != nil {
		slog.Error("failed to remove container in StopServer", "container", containerName, "error", err)
	}

	o.ports.Release(port)
	if isDynamic {
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

		containerName := s.ContainerName()
		_ = o.docker.ContainerStop(cleanupCtx, containerName, container.StopOptions{})
		_ = o.docker.ContainerRemove(cleanupCtx, containerName, container.RemoveOptions{Force: true})

		o.serversMutex.Lock()
		if _, exists := o.servers[s.ID]; exists {
			o.ports.Release(s.Port)
			if s.Network.IsDynamic {
				_ = o.networks.Release(cleanupCtx, s.ID)
			}
			_ = s.Transition(domain.EventStop)
		}
		o.serversMutex.Unlock()
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
		for {
			select {
			case <-ticker.C:
				o.GC()
			case <-o.ctx.Done():
				return
			}
		}
	}()
}

func (o *Orchestrator) ShutdownAll(ctx context.Context) {
	o.cancel()
	close(o.jobQueue)

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

			containerName := srv.ContainerName()
			fmt.Printf("Stopping container: %s\n", containerName)

			// Per-container timeout so one stuck container doesn't cancel all others.
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer stopCancel()
			_ = o.docker.ContainerStop(stopCtx, containerName, container.StopOptions{})
			_ = o.docker.ContainerRemove(stopCtx, containerName, container.RemoveOptions{Force: true})

			o.serversMutex.Lock()
			if _, exists := o.servers[srv.ID]; exists {
				o.ports.Release(srv.Port)
				if srv.Network.IsDynamic {
					_ = o.networks.Release(stopCtx, srv.ID)
				}
				_ = srv.Transition(domain.EventStop)
			}
			o.serversMutex.Unlock()
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
	if cap(o.jobQueue) > 0 {
		jobQueueFull = len(o.jobQueue) == cap(o.jobQueue)
	}
	return
}

func (o *Orchestrator) StartWorkers() {
	o.startOnce.Do(func() {
		for i := 0; i < o.cfg.Orchestrator.Workers; i++ {
			go o.worker(i)
		}
	})
}

func (o *Orchestrator) worker(_ int) {
	for s := range o.jobQueue {
		ctx, cancel := context.WithTimeout(o.ctx, time.Duration(o.cfg.Orchestrator.StartTimeout)*time.Second)

		err := o.processJob(ctx, s)
		cancel()

		if err != nil {
			_ = s.Transition(domain.EventStop)
			containerName := s.ContainerName()
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
			if err := o.docker.ContainerRemove(cleanupCtx, containerName, container.RemoveOptions{Force: true}); err != nil {
				slog.Error("failed to remove container in worker", "container", containerName, "error", err)
			}

			o.serversMutex.Lock()
			if _, exists := o.servers[s.ID]; exists {
				delete(o.servers, s.ID)
				o.ports.Release(s.Port)
				if s.Network.IsDynamic {
					_ = o.networks.Release(cleanupCtx, s.ID)
				}
			}
			o.serversMutex.Unlock()
			cancelCleanup()
		}
	}
}

func (o *Orchestrator) processJob(ctx context.Context, s *domain.Server) error {
	// Verify the server is still tracked before creating a container.
	// A concurrent StopServer may have already removed it.
	o.serversMutex.RLock()
	_, exists := o.servers[s.ID]
	o.serversMutex.RUnlock()
	if !exists {
		return fmt.Errorf("server %s removed before job started", s.ID)
	}

	if err := s.Transition(domain.EventStart); err != nil {
		return err
	}

	containerName := s.ContainerName()
	memLimit, _ := units.RAMInBytes(o.cfg.Docker.MemoryLimit)
	resp, err := o.docker.ContainerCreate(ctx, &container.Config{
		Image: o.cfg.Docker.Image,
		Labels: map[string]string{
			"minestrate.server_id": s.ID,
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
		Resources: container.Resources{
			NanoCPUs: int64(o.cfg.Docker.CPULimit * 1e9),
			Memory:   memLimit,
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
