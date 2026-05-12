package config

import (
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
		SubnetBlock string `yaml:"subnet_block"`
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

	return &cfg, nil
}
