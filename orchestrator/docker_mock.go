package orchestrator

import (
	"context"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// MockDockerClient is a mock implementation of DockerClient for development and testing purposes.
type MockDockerClient struct{}

func (m *MockDockerClient) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	return network.CreateResponse{ID: name}, nil
}
func (m *MockDockerClient) NetworkRemove(ctx context.Context, networkID string) error {
	return nil
}
func (m *MockDockerClient) NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
	return network.Inspect{}, nil
}
func (m *MockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	return container.CreateResponse{ID: containerName}, nil
}
func (m *MockDockerClient) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	return nil
}
func (m *MockDockerClient) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	return nil
}
