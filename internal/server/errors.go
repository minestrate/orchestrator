package server

import "errors"

var (
	ErrMaxServersReached = errors.New("max servers reached")
	ErrNoPortsAvailable  = errors.New("no ports available")
	ErrJobQueueFull      = errors.New("job queue full")
)
