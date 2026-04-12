package config

import (
	"flag"
	"os"
)

type Config struct {
	BackendAddr    string
	ApiKey         string
	Debug          bool
	Insecure       bool   // skip TLS — for local development only
	ArgoCDNamespace string // namespace where ArgoCD Application CRs live (default: "argocd")
}

func Load() *Config {
	backendFlag       := flag.String("backend-addr", "", "gRPC backend address")
	apiKeyFlag        := flag.String("api-key", "", "PodPulse API key")
	debugFlag         := flag.Bool("debug", false, "Enable debug logging")
	insecureFlag      := flag.Bool("insecure", false, "Disable TLS (local dev only)")
	argoNSFlag        := flag.String("argocd-namespace", "", "Namespace of ArgoCD Application CRs (default: argocd)")
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

	debug := os.Getenv("PODPULSE_DEBUG") == "true"
	if !debug {
		debug = *debugFlag
	}

	insecure := os.Getenv("PODPULSE_INSECURE") == "true"
	if !insecure {
		insecure = *insecureFlag
	}

	argoNS := os.Getenv("ARGOCD_NAMESPACE")
	if argoNS == "" {
		argoNS = *argoNSFlag
	}
	if argoNS == "" {
		argoNS = "argocd"
	}

	return &Config{
		BackendAddr:     addr,
		ApiKey:          apiKey,
		Debug:           debug,
		Insecure:        insecure,
		ArgoCDNamespace: argoNS,
	}
}
