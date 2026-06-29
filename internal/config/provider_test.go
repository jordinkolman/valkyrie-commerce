package config

import (
	"os"
	"path/filepath"

	"testing"
)

func TestLoadProviders(t *testing.T) {

	tempDir := t.TempDir()


	tests := []struct {
		name        string
		jsonContent string
		expectError bool
	}{
		{

			name: "Valid Configuration",
			jsonContent: `[
				{"name": "shopify", "idempotency_source": "header", "idempotency_key": "X-Shopify-Webhook-Id", "type": "fat"},
				{"name": "stripe", "idempotency_source": "payload", "idempotency_key": "id", "type": "thin"}
			]`,
			expectError: false,
		},
		{
			name: "Missing Required Field (IdempotencyKey)",
			jsonContent: `[
				{"name": "shopify", "idempotency_source": "header", "type": "fat"}
			]`,

			expectError: true,
		},
		{
			name: "Unsupported Idempotency Source",
			jsonContent: `[
				{"name": "shopify", "idempotency_source": "magic_database", "idempotency_key": "id", "type": "fat"}
			]`,
			expectError: true,
		},
		{

			name: "Duplicate Provider Name",
			jsonContent: `[
				{"name": "shopify", "idempotency_source": "header", "idempotency_key": "X-Shopify-Webhook-Id", "type": "fat"},
				{"name": "shopify", "idempotency_source": "payload", "idempotency_key": "id", "type": "thin"}

			]`,
			expectError: true,
		},
		{
			name: "Malformed JSON",
			jsonContent: `[{"name": "shopify", missing_quotes}]`,
			expectError: true,
		},
	}

	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			// Write the mock JSON to an isolated temporary file
			configPath := filepath.Join(tempDir, "test_config.json")
			err := os.WriteFile(configPath, []byte(tt.jsonContent), 0644)
			if err != nil {

				t.Fatalf("Failed to write temp config file: %v", err)
			}

			// Execute the loader against the temp file
			providers, err := LoadProviders(configPath)


			// Assert the expected outcome

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect an error but got: %v", err)
				}

				if len(providers) == 0 {
					t.Errorf("Expected parsed providers, got empty slice")
				}
			}
		})

	}
}
