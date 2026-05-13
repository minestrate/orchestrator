package server

import (
	"errors"
	"testing"
)

var allStates = []ServerState {
	StatePending, StateStarting, StateRunning, StateDraining, StateStopped,
}

var allEvents = []ServerEvent{
	EventStart, EventRun, EventDrain, EventStop, EventTimeout,
}

var validTransitions = map[ServerState]map[ServerEvent]ServerState{
	StatePending: {
		EventStart: StateStarting,
		EventStop:  StateStopped,
	},
	StateStarting: {
		EventRun:     StateRunning,
		EventTimeout: StateStopped,
		EventStop:    StateStopped,
	},
	StateRunning: {
		EventDrain: StateDraining,
		EventStop:  StateStopped,
	},
	StateDraining: {
		EventStop: StateStopped,
	},
	StateStopped: {}, // Terminal
}

func TestValidTransitions(t *testing.T) {
	for fromState, events := range validTransitions {
		for event, expectedState := range events {
			t.Run(string(fromState)+"_to_"+string(expectedState), func(t *testing.T) {
				s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)
				s.state = fromState
				err := s.Transition(event)

				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
				if s.State() != expectedState {
					t.Fatalf("expected state %q, got %q", expectedState, s.State())
				}
			})
		}
	}
}

func TestInvalidTransitions(t *testing.T) {
	for _, fromState := range allStates {
		for _, event := range allEvents {
			if validEvents, ok := validTransitions[fromState]; ok {
				if _, isValid := validEvents[event]; isValid {
					continue
				}
			}

			t.Run("invalid_"+string(fromState)+"_via_"+string(event), func(t *testing.T) {
				s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)
				s.state = fromState
				err := s.Transition(event)

				if err == nil {
					t.Fatalf("expected error for transition %q from %q, got nil", event, fromState)
				}

				var invalidErr *ErrInvalidTransition
				if !errors.As(err, &invalidErr) {
					t.Fatalf("expected error to be of type *ErrInvalidTransition, got %T", err)
				}

				if invalidErr.From != fromState || invalidErr.Event != event {
					t.Fatalf("error fields mismatched. expected From: %q, Event: %q. got From: %q, Event: %q",
						fromState, event, invalidErr.From, invalidErr.Event)
				}

				if s.State() != fromState {
					t.Fatalf("state mutated on invalid transition. expected %q, got %q", fromState, s.State())
				}
			})
		}
	}
}