package domain

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type ServerState string

const (
	StatePending  ServerState = "pending"
	StateStarting ServerState = "starting"
	StateRunning  ServerState = "running"
	StateDraining ServerState = "draining"
	StateStopped  ServerState = "stopped"
)

type ServerEvent string

const (
	EventStart   ServerEvent = "start"
	EventRun     ServerEvent = "run"
	EventDrain   ServerEvent = "drain"
	EventStop    ServerEvent = "stop"
	EventTimeout ServerEvent = "timeout"
)

type ErrInvalidTransition struct {
	From  ServerState
	Event ServerEvent
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid transition: state=%s event=%s", e.From, e.Event)
}

type TransitionHook func(from, to ServerState, event ServerEvent)
type transitionKey struct {
	state ServerState
	event ServerEvent
}

var transitionTable = map[transitionKey]ServerState{
	{StatePending, EventStart}: StateStarting,
	{StatePending, EventStop}:  StateStopped,

	{StateStarting, EventRun}:     StateRunning,
	{StateStarting, EventTimeout}: StateStopped,
	{StateStarting, EventStop}:    StateStopped,

	{StateRunning, EventDrain}: StateDraining,
	{StateRunning, EventStop}:  StateStopped,

	{StateDraining, EventStop}: StateStopped,
	// StateStopped is terminal: no outbound transitions.
}

type NetworkInfo struct {
	NetworkName string `json:"network_name"`
	Subnet      string `json:"subnet,omitempty"`
	Gateway     string `json:"gateway,omitempty"`
	IsDynamic   bool   `json:"is_dynamic"`
}

type Server struct {
	mu               sync.Mutex
	ID               string        `json:"id"`
	Game             string        `json:"game"`
	Players          int           `json:"players"`
	Address          string        `json:"address"`
	Port             int           `json:"port"`
	Created          time.Time     `json:"created"`
	Network          NetworkInfo   `json:"network"`
	state            ServerState
	hooks            []TransitionHook
	LastHeartbeat    time.Time     `json:"last_heartbeat"`
	HeartbeatTimeout time.Duration `json:"-"`
	TTLSeconds       int           `json:"ttl_seconds"`
	ExpiresAt        time.Time     `json:"expires_at"`
	WebhookURL       string            `json:"webhook_url,omitempty"`
	Labels           map[string]string `json:"labels"`
}

func NewServer(id, game string, players int, address string, port int) *Server {
	return &Server{
		ID:      id,
		Game:    game,
		Players: players,
		Address: address,
		Port:    port,
		Created: time.Now(),
		state:   StatePending,
	}
}

func (s *Server) OnTransition(hook TransitionHook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, hook)
}

func (s *Server) State() ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Server) Transition(event ServerEvent) error {
	s.mu.Lock()

	next, ok := transitionTable[transitionKey{s.state, event}]
	if !ok {
		err := &ErrInvalidTransition{From: s.state, Event: event}
		s.mu.Unlock()
		return err
	}

	from := s.state
	s.state = next
	hooks := s.hooks // shallow copy of the slice header; safe for iteration
	s.mu.Unlock()

	for _, h := range hooks {
		h(from, next, event)
	}
	return nil
}

func (s *Server) ContainerName() string {
	id := s.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("minestrate-%s-%s", s.Game, id)
}

func (s *Server) RecordHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastHeartbeat = time.Now()
}

func (s *Server) HeartbeatAge() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LastHeartbeat.IsZero() {
		return 0
	}
	return time.Since(s.LastHeartbeat)
}

func (s *Server) IsHeartbeatStale() bool {
	age := s.HeartbeatAge()
	if age == 0 {
		return false // never received a heartbeat
	}
	return age > s.HeartbeatTimeout
}

// ExtendTTL resets the expiration time from now + TTLSeconds.
// Does nothing if TTLSeconds is 0 (no TTL set).
func (s *Server) ExtendTTL() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.TTLSeconds > 0 {
		s.ExpiresAt = time.Now().Add(time.Duration(s.TTLSeconds) * time.Second)
	}
}

// IsExpired returns true if the server has a TTL and it has passed.
func (s *Server) IsExpired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.TTLSeconds == 0 || s.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(s.ExpiresAt)
}

func (s *Server) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return json.Marshal(struct {
		ID             string      `json:"id"`
		Game           string      `json:"game"`
		Players        int         `json:"players"`
		Address        string      `json:"address"`
		Port           int         `json:"port"`
		Created        time.Time   `json:"created"`
		Network        NetworkInfo `json:"network"`
		State          ServerState `json:"state"`
		LastHeartbeat  time.Time   `json:"last_heartbeat"`
		HeartbeatStale bool        `json:"heartbeat_stale"`
		TTLSeconds     int               `json:"ttl_seconds"`
		ExpiresAt      time.Time         `json:"expires_at"`
		Expired        bool              `json:"expired"`
		Labels         map[string]string `json:"labels"`
	}{
		ID:             s.ID,
		Game:           s.Game,
		Players:        s.Players,
		Address:        s.Address,
		Port:           s.Port,
		Created:        s.Created,
		Network:        s.Network,
		State:          s.state,
		LastHeartbeat:  s.LastHeartbeat,
		HeartbeatStale: s.LastHeartbeat.IsZero() || time.Since(s.LastHeartbeat) > s.HeartbeatTimeout,
		TTLSeconds:     s.TTLSeconds,
		ExpiresAt:      s.ExpiresAt,
		Expired:        s.TTLSeconds > 0 && !s.ExpiresAt.IsZero() && time.Now().After(s.ExpiresAt),
		Labels:         s.Labels,
	})
}

// UnmarshalJSON restores a Server from JSON, including the private state field.
func (s *Server) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID              string            `json:"id"`
		Game            string            `json:"game"`
		Players         int               `json:"players"`
		Address         string            `json:"address"`
		Port            int               `json:"port"`
		Created         time.Time         `json:"created"`
		Network         NetworkInfo       `json:"network"`
		State           ServerState       `json:"state"`
		LastHeartbeat   time.Time         `json:"last_heartbeat"`
		HeartbeatStale  bool              `json:"heartbeat_stale"`
		TTLSeconds      int               `json:"ttl_seconds"`
		ExpiresAt       time.Time         `json:"expires_at"`
		Expired         bool              `json:"expired"`
		Labels          map[string]string `json:"labels"`
		WebhookURL      string            `json:"webhook_url,omitempty"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	s.ID = aux.ID
	s.Game = aux.Game
	s.Players = aux.Players
	s.Address = aux.Address
	s.Port = aux.Port
	s.Created = aux.Created
	s.Network = aux.Network
	s.state = aux.State
	s.LastHeartbeat = aux.LastHeartbeat
	s.TTLSeconds = aux.TTLSeconds
	s.ExpiresAt = aux.ExpiresAt
	s.Labels = aux.Labels
	s.WebhookURL = aux.WebhookURL
	return nil
}
