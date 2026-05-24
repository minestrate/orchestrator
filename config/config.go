package config

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Env    string `yaml:"env"`
	Server struct {
		Port    int    `yaml:"port"`
		TLSCert string `yaml:"tls_cert"`
		TLSKey  string `yaml:"tls_key"`
	} `yaml:"server"`

	Auth struct {
		JWTSecret string `yaml:"jwt_secret"`
		TokenTTL  int    `yaml:"token_ttl"`
	} `yaml:"auth"`

	Docker struct {
		Socket      string  `yaml:"socket"`
		Image       string  `yaml:"image"`
		CPULimit    float64 `yaml:"cpu_limit"`
		MemoryLimit string  `yaml:"memory_limit"`
	} `yaml:"docker"`

	Orchestrator struct {
		Workers      int `yaml:"workers"`
		MaxServers   int `yaml:"max_servers"`
		StartTimeout int `yaml:"start_timeout"`
	} `yaml:"orchestrator"`

	Ports struct {
		RangeStart int `yaml:"range_start"`
		RangeEnd   int `yaml:"range_end"`
	} `yaml:"ports"`

	Network struct {
		Mode           string `yaml:"mode"` // "simple" or "isolated"
		SubnetBlock    string `yaml:"subnet_block"`
		DefaultNetwork string `yaml:"default_network"`
		EnableFallback bool   `yaml:"enable_fallback"`
	} `yaml:"network"`
}

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cfg Config
	err = yaml.NewDecoder(f).Decode(&cfg)
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("auth.jwt_secret must be at least 32 bytes, got %d", len(c.Auth.JWTSecret))
	}

	if c.Ports.RangeEnd <= c.Ports.RangeStart {
		return fmt.Errorf("ports.range_end (%d) must be greater than ports.range_start (%d)", c.Ports.RangeEnd, c.Ports.RangeStart)
	}

	if c.Docker.Image == "" {
		return fmt.Errorf("docker.image is required")
	}

	if c.Network.Mode == "isolated" {
		if c.Network.SubnetBlock == "" {
			return fmt.Errorf("network.subnet_block is required in isolated mode")
		}

		_, ipnet, err := net.ParseCIDR(c.Network.SubnetBlock)
		if err != nil {
			return fmt.Errorf("invalid network.subnet_block: %w", err)
		}

		ones, _ := ipnet.Mask.Size()
		if ones > 28 {
			return fmt.Errorf("network.subnet_block must be at least a /28, got /%d", ones)
		}

		numSubnets := 1 << (28 - ones)
		if c.Orchestrator.MaxServers > numSubnets {
			fmt.Fprintf(os.Stderr, "WARNING: orchestrator.max_servers (%d) exceeds available /28 subnets in %s (%d)\n",
				c.Orchestrator.MaxServers, c.Network.SubnetBlock, numSubnets)
		}
	}

	if c.Network.Mode == "simple" && c.Network.DefaultNetwork == "" {
		return fmt.Errorf("network.default_network is required in simple mode")
	}

	return nil
}
