// Package orchestrator re-exports dockerclient.Client and dockerclient.MockClient
// for convenience so existing callers continue to compile without changing imports.
package core

import (
	"github.com/mitsuakki/minestrate/core/dockerclient"
)

// DockerClient is the canonical Docker API interface.
// Deprecated: import github.com/mitsuakki/minestrate/core/dockerclient directly.
type DockerClient = dockerclient.Client

// MockDockerClient is a no-op implementation of DockerClient.
// Deprecated: use dockerclient.MockClient instead.
type MockDockerClient = dockerclient.MockClient
