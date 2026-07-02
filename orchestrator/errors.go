package orchestrator

import "errors"

var (
	ErrMaxServersReached = errors.New("max servers reached")
	ErrJobQueueFull      = errors.New("job queue full")
	ErrServerNotFound    = errors.New("server not found")
	ErrServerNotRunning  = errors.New("server not in running state")
	ErrNoPortsAvailable  = errors.New("no ports available")
)
