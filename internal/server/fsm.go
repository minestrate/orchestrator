package server

import (
	"encoding/json"
	"fmt"
	"sync"
)

// ServerState represents the lifecycle state of a server
type ServerState string

const (
	StatePending  ServerState = "pending"
	StateStarting ServerState = "starting"
	StateRunning  ServerState = "running"
	StateDraining ServerState = "draining"
	StateStopped  ServerState = "stopped"
)

// ServerEvent represents a trigger that causes a transition
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
	return fmt.Sprintf("invalid transition from=%s event=%s", e.From, e.Event)
}

// Server holds the state of a server record and ensures that transitions
// follow the defined state machine. No mutation outside the Transition
// method is permitted.
type Server struct {
	mutex   sync.Mutex
	ID      string      `json:"id"`
	Game    string      `json:"game"`
	Players int         `json:"players"`
	Address string      `json:"address"`
	Port    int         `json:"port"`
	state   ServerState
}

func NewServer(id, game string, players int, address string, port int) *Server {
	return &Server{
		ID:      id,
		Game:    game,
		Players: players,
		Address: address,
		Port:    port,
		state:   StatePending,
	}
}

func (s *Server) State() ServerState {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.state
}

func (s *Server) MarshalJSON() ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return json.Marshal(struct {
		ID      string      `json:"id"`
		Game    string      `json:"game"`
		Players int         `json:"players"`
		Address string      `json:"address"`
		Port    int         `json:"port"`
		State   ServerState `json:"state"`
	}{
		ID:      s.ID,
		Game:    s.Game,
		Players: s.Players,
		Address: s.Address,
		Port:    s.Port,
		State:   s.state,
	})
}

func (s *Server) Transition(event ServerEvent) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	switch s.state {
		case StatePending:
			if event == EventStart {
				s.state = StateStarting
				return nil
			}
			if event == EventStop {
				s.state = StateStopped
				return nil
			}

		case StateStarting:
			if event == EventRun {
				s.state = StateRunning
				return nil
			}
			if event == EventTimeout {
				s.state = StateStopped
				return nil
			}
		case StateRunning:
			if event == EventDrain {
				s.state = StateDraining
				return nil
			}
		case StateDraining:
			if event == EventStop {
				s.state = StateStopped
				return nil
			}
		case StateStopped:
			// Terminal state: no outbound transitions permitted.
	}

	return &ErrInvalidTransition{
		From:  s.state,
		Event: event,
	}
}