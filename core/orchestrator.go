package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

// ServerHealth summarizes the health of a single server.
type ServerHealth struct {
	ServerID         string  `json:"server_id"`
	State            string  `json:"state"`
	ContainerRunning bool    `json:"container_running"`
	HeartbeatAgeSec  float64 `json:"heartbeat_age_seconds"`
	HeartbeatStale   bool    `json:"heartbeat_stale"`
	Healthy          bool    `json:"healthy"`
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

	// Clean up any orphaned containers from a previous crash.
	o.cleanupOrphans()

	return o, nil
}

// cleanupOrphans stops and removes any containers from a previous orchestrator run.
func (o *Orchestrator) cleanupOrphans() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containers, err := o.docker.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		slog.Warn("failed to list orphaned containers on startup", "error", err)
		return
	}

	if len(containers) == 0 {
		return
	}

	slog.Info("cleaning up orphaned containers from previous run", "count", len(containers))
	for _, c := range containers {
		// Only remove containers that belong to minestrate.
		if c.Labels == nil || c.Labels["minestrate.server_id"] == "" {
			continue
		}
		slog.Info("removing orphaned container", "container", c.ID, "name", c.Names)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = o.docker.ContainerStop(cleanupCtx, c.ID, container.StopOptions{})
		_ = o.docker.ContainerRemove(cleanupCtx, c.ID, container.RemoveOptions{Force: true})
		cancel()
	}
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

func (o *Orchestrator) CreateServer(ctx context.Context, game string, players int, networkName string, ttlSeconds int, webhookURL string, labels map[string]string) (*domain.Server, error) {
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
	s.HeartbeatTimeout = time.Duration(o.cfg.Orchestrator.HeartbeatTimeout) * time.Second
	s.TTLSeconds = ttlSeconds
	if ttlSeconds > 0 {
		s.ExpiresAt = s.Created.Add(time.Duration(ttlSeconds) * time.Second)
	}
	s.WebhookURL = webhookURL
	if webhookURL != "" {
		s.OnTransition(o.webhookHook(s))
	}
	if labels != nil {
		s.Labels = labels
	}
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
	var expiredIDs []string

	o.serversMutex.Lock()
	o.updateMetrics()
	for id, s := range o.servers {
		if s.State() == domain.StateStopped {
			delete(o.servers, id)
			continue
		}
		// Log stale heartbeats for running servers as an early warning.
		if s.State() == domain.StateRunning && s.IsHeartbeatStale() {
			slog.Warn("server heartbeat stale",
				"server_id", s.ID,
				"heartbeat_age_seconds", s.HeartbeatAge().Seconds(),
			)
		}
		// Collect expired servers for draining.
		if s.State() == domain.StateRunning && s.IsExpired() {
			expiredIDs = append(expiredIDs, id)
			continue
		}
		// Check max server lifetime.
		if o.cfg.Orchestrator.MaxServerLifetime > 0 && s.State() == domain.StateRunning {
			age := time.Since(s.Created)
			if age > time.Duration(o.cfg.Orchestrator.MaxServerLifetime)*time.Second {
				expiredIDs = append(expiredIDs, id)
			}
		}
	}
	o.serversMutex.Unlock()

	// Drain expired servers outside the lock to avoid blocking.
	for _, id := range expiredIDs {
		slog.Info("server lifetime exceeded, draining", "server_id", id)
		if err := o.ShutdownServer(o.ctx, id); err != nil {
			slog.Error("failed to drain expired server", "server_id", id, "error", err)
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
	return o.ListServersByLabels(nil)
}

// ListServersByLabels returns non-stopped servers. If labels is non-empty, only
// servers whose Labels map contains all the requested key:value pairs are returned.
func (o *Orchestrator) ListServersByLabels(labels map[string]string) []*domain.Server {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()
	list := make([]*domain.Server, 0, len(o.servers))
	for _, s := range o.servers {
		if s.State() == domain.StateStopped {
			continue
		}
		if !matchLabels(s.Labels, labels) {
			continue
		}
		list = append(list, s)
	}
	return list
}

// matchLabels returns true if serverLabels contains all required key:value pairs.
func matchLabels(serverLabels, filter map[string]string) bool {
	if len(filter) == 0 {
		return true
	}
	if serverLabels == nil {
		return false
	}
	for k, v := range filter {
		if serverLabels[k] != v {
			return false
		}
	}
	return true
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

// ServerHealth checks the health of a server by inspecting its Docker container
// and heartbeat freshness. Returns the health status or an error if the server
// is not found or the container inspect fails.
func (o *Orchestrator) ServerHealth(ctx context.Context, id string) (ServerHealth, error) {
	o.serversMutex.RLock()
	s, ok := o.servers[id]
	o.serversMutex.RUnlock()

	if !ok {
		return ServerHealth{}, ErrServerNotFound
	}

	state := s.State()
	containerName := s.ContainerName()

	insp, err := o.docker.ContainerInspect(ctx, containerName)
	containerRunning := false
	if err == nil {
		containerRunning = insp.State != nil && insp.State.Running
	}

	heartbeatAge := s.HeartbeatAge()
	heartbeatStale := s.IsHeartbeatStale()

	// Healthy: container is running AND heartbeat is not stale.
	// If state != running, the container shouldn't be running yet/anymore — not "healthy"
	// but also not an error.
	healthy := state == domain.StateRunning && containerRunning && !heartbeatStale

	return ServerHealth{
		ServerID:         id,
		State:            string(state),
		ContainerRunning: containerRunning,
		HeartbeatAgeSec:  heartbeatAge.Seconds(),
		HeartbeatStale:   heartbeatStale,
		Healthy:          healthy,
	}, nil
}

// RecordHeartbeat records a heartbeat for the given server.
func (o *Orchestrator) RecordHeartbeat(id string) error {
	o.serversMutex.RLock()
	s, ok := o.servers[id]
	o.serversMutex.RUnlock()

	if !ok {
		return ErrServerNotFound
	}

	s.RecordHeartbeat()
	return nil
}

// ExtendServerTTL resets the expiry timer for a server. Returns ErrServerNotFound
// or ErrServerNoTTL if the server was created without a TTL.
func (o *Orchestrator) ExtendServerTTL(id string) error {
	o.serversMutex.RLock()
	s, ok := o.servers[id]
	o.serversMutex.RUnlock()

	if !ok {
		return ErrServerNotFound
	}
	if s.TTLSeconds == 0 {
		return ErrServerNoTTL
	}

	s.ExtendTTL()
	return nil
}

// webhookHook returns a TransitionHook that fires an async HTTP POST to the
// server's webhook URL with the transition event as JSON.
func (o *Orchestrator) webhookHook(s *domain.Server) domain.TransitionHook {
	return func(from, to domain.ServerState, event domain.ServerEvent) {
		// Only fire for terminal/meaningful transitions.
		eventName := webhookEventName(from, to, event)
		if eventName == "" {
			return
		}

		payload := map[string]any{
			"event":     eventName,
			"server_id": s.ID,
			"game":      s.Game,
			"players":   s.Players,
			"address":   s.Address,
			"port":      s.Port,
			"from":      string(from),
			"to":        string(to),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}

		body, err := json.Marshal(payload)
		if err != nil {
			slog.Error("webhook marshal failed", "server_id", s.ID, "error", err)
			return
		}

		// Fire asynchronously so the FSM transition is never blocked.
		go func(url string, body []byte) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				slog.Error("webhook request creation failed", "server_id", s.ID, "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				slog.Warn("webhook delivery failed", "server_id", s.ID, "event", eventName, "error", err)
				return
			}
			resp.Body.Close()

			if resp.StatusCode >= 400 {
				slog.Warn("webhook returned error status", "server_id", s.ID, "event", eventName, "status", resp.StatusCode)
			}
		}(s.WebhookURL, body)
	}
}

// webhookEventName maps an FSM transition to a webhook event name.
// Returns empty string for transitions that should not fire a webhook.
func webhookEventName(from, to domain.ServerState, event domain.ServerEvent) string {
	switch {
	case from == domain.StateStarting && to == domain.StateRunning:
		return "server.running"
	case from == domain.StateStarting && to == domain.StateStopped:
		// Startup failure (timeout, explicit stop, or worker error).
		return "server.timeout"
	case to == domain.StateDraining:
		return "server.draining"
	case to == domain.StateStopped:
		return "server.stopped"
	default:
		return ""
	}
}

func (o *Orchestrator) StartWorkers() {
	o.startOnce.Do(func() {
		for i := 0; i < o.cfg.Orchestrator.Workers; i++ {
			go o.worker(i)
		}
	})
}

func (o *Orchestrator) worker(_ int) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("worker panicked, recovering", "panic", r)
		}
	}()

	for s := range o.jobQueue {
		ctx, cancel := context.WithTimeout(o.ctx, time.Duration(o.cfg.Orchestrator.StartTimeout)*time.Second)

		start := time.Now()
		err := o.processJob(ctx, s)
		observeSpawn(time.Since(start).Seconds())
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
