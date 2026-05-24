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
	{StatePending, EventStart}:    StateStarting,
	{StatePending, EventStop}:     StateStopped,
	{StateStarting, EventRun}:     StateRunning,
	{StateStarting, EventTimeout}: StateStopped,
	{StateStarting, EventStop}:    StateStopped,
	{StateRunning, EventDrain}:    StateDraining,
	{StateRunning, EventStop}:     StateStopped,
	{StateDraining, EventStop}:    StateStopped,

	// StateStopped is terminal: no outbound transitions.
}

type Server struct {
	mu      sync.Mutex
	ID      string                 `json:"id"`
	Game    string                 `json:"game"`
	Players int                    `json:"players"`
	Address string                 `json:"address"`
	Port    int                    `json:"port"`
	Created time.Time              `json:"created"`
	Network map[string]interface{} `json:"network"`
	state   ServerState
	hooks   []TransitionHook
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

func (s *Server) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return json.Marshal(struct {
		ID      string                 `json:"id"`
		Game    string                 `json:"game"`
		Players int                    `json:"players"`
		Address string                 `json:"address"`
		Port    int                    `json:"port"`
		Created time.Time              `json:"created"`
		Network map[string]interface{} `json:"network"`
		State   ServerState            `json:"state"`
	}{
		ID:      s.ID,
		Game:    s.Game,
		Players: s.Players,
		Address: s.Address,
		Port:    s.Port,
		Created: s.Created,
		Network: s.Network,
		State:   s.state,
	})
}
