package config

import (
    "flag"
    "os"
)

type Config struct {
    BackendAddr string
}

func Load() *Config {
    backendFlag := flag.String("backend-addr", "", "gRPC backend address (e.g. localhost:50051)")
    flag.Parse()

    // Environment variable takes priority over flag
    addr := os.Getenv("PODPULSE_BACKEND_ADDR")
    if addr == "" {
        addr = *backendFlag
    }
    if addr == "" {
        addr = "localhost:50051" // default for local dev
    }

    return &Config{
        BackendAddr: addr,
    }
}