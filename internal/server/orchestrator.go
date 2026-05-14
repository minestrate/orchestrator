package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mitsuakki/minestrate/config"
)

type Orchestrator struct {
	cfg          *config.Config
	servers      map[string]*Server
	serversMutex sync.RWMutex
	ports        *PortAllocator
	networks     NetworkManager
	jobQueue     chan *Server
}

func NewOrchestrator(cfg *config.Config, docker DockerClient) (*Orchestrator, error) {
	var nm NetworkManager
	var err error

	switch cfg.Network.Mode {
	case "simple":
		nm = NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
	case "isolated":
		nm, err = NewIsolatedSubnetManager(docker, cfg.Network.SubnetBlock)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidNetworkMode
	}

	if cfg.Network.EnableFallback && cfg.Network.Mode == "isolated" {
		secondary := NewSimpleNetworkManager(cfg.Network.DefaultNetwork)
		nm = NewFallbackNetworkManager(nm, secondary)
	}

	o := &Orchestrator{
		cfg:      cfg,
		servers:  make(map[string]*Server),
		ports:    NewPortAllocator(cfg.Ports.RangeStart, cfg.Ports.RangeEnd),
		networks: nm,
		jobQueue: make(chan *Server, 100),
	}

	return o, nil
}

func (o *Orchestrator) CreateServer(ctx context.Context, game string, players int) (*Server, error) {
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

	netCfg, err := o.networks.Allocate(ctx, id)
	if err != nil {
		o.ports.Release(port)
		o.serversMutex.Unlock()
		return nil, err
	}

	s := NewServer(id, game, players, "127.0.0.1", port)
	s.Network = netCfg

	o.servers[id] = s
	o.serversMutex.Unlock()

	select {
	case o.jobQueue <- s:
		return s, nil
	default:
		// Cleanup if queue is full
		o.serversMutex.Lock()
		delete(o.servers, id)
		o.serversMutex.Unlock()
		o.ports.Release(port)
		_ = o.networks.Release(ctx, id)
		return nil, ErrJobQueueFull
	}
}

func (o *Orchestrator) StopServer(ctx context.Context, id string) error {
	o.serversMutex.Lock()
	defer o.serversMutex.Unlock()

	s, ok := o.servers[id]
	if !ok {
		return fmt.Errorf("server %s not found", id)
	}

	if err := s.Transition(EventStop); err != nil {
		return err
	}

	delete(o.servers, id)
	o.ports.Release(s.Port)
	return o.networks.Release(ctx, id)
}

func (o *Orchestrator) GetServer(id string) (*Server, bool) {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()
	s, ok := o.servers[id]
	return s, ok
}

func (o *Orchestrator) ListServers() []*Server {
	o.serversMutex.RLock()
	defer o.serversMutex.RUnlock()
	list := make([]*Server, 0, len(o.servers))
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
		fmt.Printf("Worker %d starting server %s\n", id, s.ID)
		_ = s.Transition(EventStart)
		
		// Simulate startup
		time.Sleep(100 * time.Millisecond)
		_ = s.Transition(EventRun)
	}
}
