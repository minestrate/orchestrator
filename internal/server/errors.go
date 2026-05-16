package server

import "errors"

var (
	ErrMaxServersReached = errors.New("max servers reached")
	ErrNoPortsAvailable  = errors.New("no ports available")
	ErrNoSubnetsAvailable = errors.New("no subnets available")
	ErrJobQueueFull      = errors.New("job queue full")
	ErrNetworkNotFound   = errors.New("network not found")
	ErrInvalidNetworkMode = errors.New("invalid network mode")
	ErrServerNotFound    = errors.New("server not found")
	ErrServerNotRunning   = errors.New("server not in running state")
)
