package core

import (
	"fmt"
	"math"
	"os"

	"github.com/docker/go-units"
	"gopkg.in/yaml.v3"
)

// shannonEntropy calculates the Shannon entropy of a string.
func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	freq := make(map[rune]float64)
	for _, r := range s {
		freq[r]++
	}
	entropy := 0.0
	length := float64(len(s))
	for _, count := range freq {
		p := count / length
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// chiSquareUniformity calculates the chi-square statistic for character distribution uniformity.
// Values closer to the number of distinct characters are better.
func chiSquareUniformity(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	counts := make(map[rune]float64)
	for _, r := range s {
		counts[r]++
	}

	expected := float64(len(s)) / float64(len(counts))
	chiSquare := 0.0
	for _, count := range counts {
		chiSquare += math.Pow(count-expected, 2) / expected
	}
	return chiSquare
}

type Config struct {
	DataDir string `yaml:"data_dir"`
	Server  struct {
		Port    int    `yaml:"port"`
		TLSCert           string `yaml:"tls_cert"`
		TLSKey            string `yaml:"tls_key"`
		AdvertisedAddress string `yaml:"advertised_address"`
	} `yaml:"server"`

	Auth struct {
		JWTSecret string `yaml:"jwt_secret"`
		TokenTTL  int    `yaml:"token_ttl"`
		RateLimit struct {
			RefillRate float64 `yaml:"refill_rate"`
			Capacity   int     `yaml:"capacity"`
		} `yaml:"rate_limit"`
	} `yaml:"auth"`

	Docker struct {
		Socket      string  `yaml:"socket"`
		Image       string  `yaml:"image"`
		CPULimit    float64 `yaml:"cpu_limit"`
		MemoryLimit string  `yaml:"memory_limit"`
	} `yaml:"docker"`

	Orchestrator struct {
		Workers             int            `yaml:"workers"`
		MaxServers          int            `yaml:"max_servers"`
		StartTimeout        int            `yaml:"start_timeout"`
		HeartbeatTimeout    int            `yaml:"heartbeat_timeout"`     // seconds, default 30
		MaxServerLifetime   int            `yaml:"max_server_lifetime"`   // seconds, 0=unlimited
		MaxServersPerLabel  map[string]int `yaml:"max_servers_per_label"` // label key → max count
	} `yaml:"orchestrator"`

	Ports struct {
		RangeStart int `yaml:"range_start"`
		RangeEnd   int `yaml:"range_end"`
	} `yaml:"ports"`

	Network struct {
		DefaultNetwork string `yaml:"default_network"`
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
	if shannonEntropy(c.Auth.JWTSecret) < 4.0 {
		return fmt.Errorf("auth.jwt_secret is not secure enough (too low entropy)")
	}
	if chiSquareUniformity(c.Auth.JWTSecret) > 50.0 {
		return fmt.Errorf("auth.jwt_secret is not secure enough (statistically non-uniform)")
	}

	if c.Ports.RangeEnd <= c.Ports.RangeStart {
		return fmt.Errorf("ports.range_end (%d) must be greater than ports.range_start (%d)", c.Ports.RangeEnd, c.Ports.RangeStart)
	}

	if c.Docker.Image == "" {
		return fmt.Errorf("docker.image is required")
	}

	if c.Docker.MemoryLimit != "" {
		if _, err := units.RAMInBytes(c.Docker.MemoryLimit); err != nil {
			return fmt.Errorf("docker.memory_limit: %w", err)
		}
	}

	if c.Network.DefaultNetwork == "" {
		return fmt.Errorf("network.default_network is required")
	}

	return nil
}
