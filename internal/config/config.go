package config

import (
	"flag"
	"os"
)

type Config struct {
	BackendAddr string
	ApiKey      string
}

func Load() *Config {
	backendFlag := flag.String("backend-addr", "", "gRPC backend address")
	apiKeyFlag := flag.String("api-key", "", "PodPulse API key")
	flag.Parse()

	addr := os.Getenv("PODPULSE_BACKEND_ADDR")
	if addr == "" {
		addr = *backendFlag
	}
	if addr == "" {
		addr = "localhost:5050"
	}

	apiKey := os.Getenv("PODPULSE_API_KEY")
	if apiKey == "" {
		apiKey = *apiKeyFlag
	}

	return &Config{
		BackendAddr: addr,
		ApiKey:      apiKey,
	}
}
