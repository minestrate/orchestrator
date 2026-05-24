package config

import (
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Config)
		wantErr bool
	}{
		{
			name: "Valid simple config",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = "test-image"
				c.Network.Mode = "simple"
				c.Network.DefaultNetwork = "default"
			},
			wantErr: false,
		},
		{
			name: "JWT secret too short",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "too-short"
			},
			wantErr: true,
		},
		{
			name: "Invalid port range",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 2000
				c.Ports.RangeEnd = 1000
			},
			wantErr: true,
		},
		{
			name: "Missing docker image",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = ""
			},
			wantErr: true,
		},
		{
			name: "Isolated mode missing subnet block",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = "test"
				c.Network.Mode = "isolated"
				c.Network.SubnetBlock = ""
			},
			wantErr: true,
		},
		{
			name: "Isolated mode invalid subnet block",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = "test"
				c.Network.Mode = "isolated"
				c.Network.SubnetBlock = "invalid"
			},
			wantErr: true,
		},
		{
			name: "Isolated mode subnet too small",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = "test"
				c.Network.Mode = "isolated"
				c.Network.SubnetBlock = "192.168.1.0/30"
			},
			wantErr: true,
		},
		{
			name: "Simple mode missing default network",
			setup: func(c *Config) {
				c.Auth.JWTSecret = "this-is-a-very-long-secret-key-32-bytes"
				c.Ports.RangeStart = 1000
				c.Ports.RangeEnd = 2000
				c.Docker.Image = "test"
				c.Network.Mode = "simple"
				c.Network.DefaultNetwork = ""
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			tt.setup(c)
			if err := c.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
