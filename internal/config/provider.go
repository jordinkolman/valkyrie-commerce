// Package config handles the ingestion and validation of external configuration files.
package config

import (
	"fmt"
	"encoding/json"
	"os"
)

// WebhookType dictates which Redis stream the payload will be routed to.
type WebhookType string

// Provider defines the routing and idempotency extraction rules for a specific third-party webhook source.
type Provider struct {
	Name string `json:"name"`
	SignatureHeader string `json:"signature_header"`
	IdempotencySource string `json:"idempotency_source"`
	IdempotencyKey string `json:"idempotency_key"`
	Type WebhookType `json:"type"`
}

const (
	Fat WebhookType = "fat"
	Thin WebhookType = "thin"
)

// LoadProviders reads a JSON configuration file from disk, parses the provider definitions, 
// and strictly validates them against supported parameters to prevent runtime routing panics.
func LoadProviders(filepath string) ([]Provider, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()

	var providers []Provider
	if err = dec.Decode(&providers); err != nil {
		return nil, fmt.Errorf("failed to decode provider config: %w", err)
	}

	seen := make(map[string]struct{}, len(providers))

	for i, p := range providers {
		if p.Name == "" || p.IdempotencyKey == "" {
			return nil, fmt.Errorf("provider[%d]: missing required fields", i)
		}
		switch p.IdempotencySource {
		case "header", "payload":
		default:
			return nil, fmt.Errorf("provider[%d]: unsupported idempotency_source %q", i, p.IdempotencySource)
		}
		switch p.Type {
		case Fat, Thin:
		default:
			return nil, fmt.Errorf("provider[%d]: unsupported type %q", i, p.Type)
		}
		if _, dup := seen[p.Name]; dup {
			return nil, fmt.Errorf("duplicate provider name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}

	return providers, nil
}
