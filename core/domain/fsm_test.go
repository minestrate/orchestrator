package domain

import (
	"errors"
	"testing"
	"time"
)

var allStates = []ServerState{
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

func TestHeartbeatInitialAge(t *testing.T) {
	s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)
	s.HeartbeatTimeout = 10 * time.Second

	if age := s.HeartbeatAge(); age != 0 {
		t.Fatalf("initial heartbeat age should be 0, got %v", age)
	}
	if s.IsHeartbeatStale() {
		t.Fatal("initial heartbeat should not be stale")
	}
}

func TestHeartbeatRecordAndAge(t *testing.T) {
	s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)
	s.HeartbeatTimeout = 10 * time.Second

	s.RecordHeartbeat()

	age := s.HeartbeatAge()
	if age <= 0 {
		t.Fatalf("heartbeat age should be > 0 after recording, got %v", age)
	}
	if s.IsHeartbeatStale() {
		t.Fatal("fresh heartbeat should not be stale")
	}
}

func TestTTLExpiry(t *testing.T) {
	s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)

	// No TTL: never expires.
	if s.IsExpired() {
		t.Fatal("server without TTL should not be expired")
	}

	// Set TTL and check expiry.
	s.TTLSeconds = 1
	s.ExpiresAt = time.Now().Add(-1 * time.Second) // expired 1s ago
	if !s.IsExpired() {
		t.Fatal("server with past expiry should be expired")
	}

	// Extend TTL.
	s.ExtendTTL()
	if s.IsExpired() {
		t.Fatal("server should not be expired after ExtendTTL")
	}
	if s.ExpiresAt.Before(time.Now()) {
		t.Fatal("ExpiresAt should be in the future after ExtendTTL")
	}

	// Zero TTL: ExtendTTL is a no-op.
	s.TTLSeconds = 0
	oldExpiry := s.ExpiresAt
	s.ExtendTTL()
	if !s.ExpiresAt.Equal(oldExpiry) {
		t.Fatal("ExtendTTL with TTLSeconds=0 should not change ExpiresAt")
	}
}

func TestHeartbeatStale(t *testing.T) {
	s := NewServer("test", "skywars", 8, "127.0.0.1", 19132)
	s.HeartbeatTimeout = 1 * time.Nanosecond

	s.RecordHeartbeat()
	time.Sleep(10 * time.Millisecond)

	if !s.IsHeartbeatStale() {
		t.Fatal("heartbeat should be stale after timeout")
	}
}
