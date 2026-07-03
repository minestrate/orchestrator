// Package dockerclient defines the canonical Docker API interface
// used throughout the orchestrator. Both the orchestrator and the
// internal allocator depend on this single interface, avoiding
// duplicate definitions.
package dockerclient

import (
	"context"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Client wraps all Docker API calls used by the orchestrator.
type Client interface {
	NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkRemove(ctx context.Context, networkID string) error
	NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)

	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

// MockClient is a no-op implementation of Client for development and testing.
type MockClient struct{}

func (m *MockClient) NetworkCreate(_ context.Context, name string, _ network.CreateOptions) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}

func (m *MockClient) NetworkRemove(_ context.Context, _ string) error {
	return nil
}

func (m *MockClient) NetworkInspect(_ context.Context, _ string, _ network.InspectOptions) (network.Inspect, error) {
	return network.Inspect{}, nil
}

func (m *MockClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	return container.CreateResponse{ID: containerName}, nil
}

func (m *MockClient) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return nil
}

func (m *MockClient) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	return nil
}

func (m *MockClient) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	return nil
}
