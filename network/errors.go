package network

import "errors"

var (
	ErrNoPortsAvailable   = errors.New("no ports available")
	ErrNoSubnetsAvailable = errors.New("no subnets available")
	ErrNetworkNotFound    = errors.New("network not found")
	ErrInvalidNetworkMode = errors.New("invalid network mode")
)
