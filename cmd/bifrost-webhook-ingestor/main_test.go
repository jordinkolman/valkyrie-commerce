package main


import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// setupTestEnvironment initializes an in-memory Redis instance and binds the Bifrost server.
func setupTestEnvironment(t *testing.T) (*Server, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to initialize miniredis: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})


	return &Server{redisClient: client},  mr
}

func TestWebhookIngestion(t *testing.T) {
	srv, mr := setupTestEnvironment(t)
	defer mr.Close()
	defer srv.redisClient.Close()

	shopifyProvider := Provider{
		Name: "shopify",
		IdempotencySource: "header",
		IdempotencyKey: "X-Shopify-Webhook-Id",
		Type: Fat,
	}

	stripeProvider := Provider{
		Name: "stripe",
		IdempotencySource: "payload",
		IdempotencyKey: "id",
		Type: Thin,
	}

	tests := []struct {

		name               string
		provider           Provider
		payload            []byte
		headers            map[string]string
		expectedStatus     int
		expectedStreamName string
		expectedStreamLen  int
		duplicateExecution bool
	}{
		{
			name:               "Valid Fat Webhook (Shopify)",
			provider:           shopifyProvider,

			payload:            []byte(`{"order_id": 1001, "total": 45.00}`),
			headers:            map[string]string{"X-Shopify-Webhook-Id": "uuid-1234"},

			expectedStatus:     http.StatusOK,
			expectedStreamName: "incoming_webhooks",
			expectedStreamLen:  1,
		},
		{
			name:               "Atomic Idempotency Drop (Duplicate Payload)",
			provider:           shopifyProvider,
			payload:            []byte(`{"order_id": 1002, "total": 55.00}`),

			headers:            map[string]string{"X-Shopify-Webhook-Id": "uuid-5678"},
			expectedStatus:     http.StatusOK,
			expectedStreamName: "incoming_webhooks",
			expectedStreamLen:  1, // Failsafe: Should remain 1 even after duplicate execution
			duplicateExecution: true,
		},
		{
			name:               "Keyless Webhook Fallback",
			provider:           shopifyProvider,
			payload:            []byte(`{"order_id": 1003, "total": 65.00}`),
			headers:            map[string]string{}, // Missing Header
			expectedStatus:     http.StatusOK,
			expectedStreamName: "incoming_webhooks",
			expectedStreamLen:  1,
		},
		{
			name:               "Valid Thin Webhook Routing (Stripe)",
			provider:           stripeProvider,
			payload:            []byte(`{"id": "evt_9999:, "event_type": "charge.succeeded"}`),
			headers:            map[string]string{},
			expectedStatus:     http.StatusOK,
			expectedStreamName: "thin_webhooks",
			expectedStreamLen:  1,
		},
		{
			name:               "OOM Protection (Payload Exceeds 5MB)",

			provider:           shopifyProvider,
			payload:            make([]byte, maxPayloadSize+1), // 5MB + 1 Byte
			headers:            map[string]string{"X-Shopify-Webhook-Id": "uuid-massive"},
			expectedStatus:     http.StatusBadRequest,
			expectedStreamName: "incoming_webhooks",

			expectedStreamLen:  0,
		},
	}


	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Wipe the in-memory redis state between tests to prevent stream pollution
			mr.FlushAll()

			handler := srv.buildWebhookHandler(tt.provider)

			// Execute Initial Request
			req := httptest.NewRequest(http.MethodPost, "/webhook/"+tt.provider.Name, bytes.NewReader(tt.payload))
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Assert Initial Status
			if rr.Code != tt.expectedStatus {
				t.Errorf("expected HTTP %d, got HTTP %d", tt.expectedStatus, rr.Code)
			}

			// Execute Duplicate Request if mandated by the test parameters
			if tt.duplicateExecution {
				reqDup := httptest.NewRequest(http.MethodPost, "/webhook/"+tt.provider.Name, bytes.NewReader(tt.payload))
				for k, v := range tt.headers {
					reqDup.Header.Set(k, v)
				}
				rrDup := httptest.NewRecorder()
				handler.ServeHTTP(rrDup, reqDup)

				// Second response must still return 200 OK to the provider, but NOT write to Redis
				if rrDup.Code != tt.expectedStatus {
					t.Errorf("duplicate execution expected HTTP %d, got HTTP %d", tt.expectedStatus, rrDup.Code)
				}
			}

			// Assert Redis Stream State
			if tt.expectedStreamLen >= 0 {
				ctx := context.Background()
				messages, err := srv.redisClient.XRange(ctx, tt.expectedStreamName, "-", "+").Result()

				// If we expect 0 messages, it's valid for the stream to not exist
				if err != nil && err != redis.Nil {
					t.Fatalf("failed to query redis stream: %v", err)
				}

				actualLen := len(messages)

				if actualLen != tt.expectedStreamLen {
					t.Errorf("expected %d messages in stream '%s', found %d", tt.expectedStreamLen, tt.expectedStreamName, actualLen)
				}
			}
		})
	}
}
