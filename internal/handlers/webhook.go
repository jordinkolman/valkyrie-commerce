// Package handlers contains the HTTP routing logic, payload validation, and database ingestion mechanics.
package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"

	"github.com/jordinkolman/valkyrie-commerce/internal/config"
)

// Server holds the dependencies required by HTTP handlers, such as the active Redis connection pool.
type Server struct {
	redisClient *redis.Client
}

const maxPayloadSize = 5 * 1024 * 1024


var ingestScript = redis.NewScript(`
  local lock_key = KEYS[1]
  local stream_name = KEYS[2]
    
  local ttl = ARGV[1]
  local max_len = ARGV[2]
  local webhook_id = ARGV[3]
  local payload = ARGV[4]

  local lock_result = redis.call('SET', lock_key, 'locked', 'NX', 'EX', ttl)

  if not lock_result then
    return 0
  end

  redis.call('XADD', stream_name, 'MAXLEN', '~', max_len, '*', 'webhook_id', webhook_id, 'payload', payload)

  return 1
`)

// NewServer acts as the constructor for the handler suite, safely injecting the necessary infrastructure dependencies.
func NewServer(client *redis.Client) *Server {
	return &Server{
		redisClient: client,
	}
}

// BuildWebhookHandler returns an http.HandlerFunc bound to a specific provider's ruleset.
// It executes payload size validation, dynamic idempotency extraction, and atomic Redis stream insertion.
func (srv *Server) BuildWebhookHandler(p config.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Received webhook", "webhook_type", p.Type, "provider", p.Name, "remote_addr", r.RemoteAddr)
		streamName := "incoming_webhooks"
		if p.Type == config.Thin {
			streamName = "thin_webhooks"
		}

		srv.ingestToStream(w, r, streamName, p)
	}
}

// ingestToStream handles the core ingestion pipeline for an incoming webhook.
// It enforces memory safety via bounded body reading, extracts the idempotency key 
// based on the provider's configuration (header or payload), and executes an atomic 
// Redis Lua script to guarantee deduplication and stream insertion without race conditions.
func (srv *Server) ingestToStream(w http.ResponseWriter, r *http.Request, streamName string, p config.Provider) {
	// Enforce max payload size to protect memory
	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadSize)

	payloadBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Error reading body", "error", err)
		http.Error(w, "Payload too large or malformed", http.StatusBadRequest)
		return
	}

	var idempKey string
	if p.IdempotencySource == "header" {
		idempKey = r.Header.Get(p.IdempotencyKey)
	} else if p.IdempotencySource == "payload" {
		idempKey = gjson.GetBytes(payloadBytes, p.IdempotencyKey).String()
	}

	missingIdempotencyKey := idempKey == ""
	if missingIdempotencyKey {
		slog.Warn("Warning: Missing idempotency key", "provider", p.Name)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
	defer cancel()

	maxStreamLength := 10000

	if missingIdempotencyKey {
		_, err := srv.redisClient.XAdd(ctx, &redis.XAddArgs{
			Stream: streamName,
			MaxLen: int64(maxStreamLength),
			Approx: true,
			Values: map[string]any{"webhook_id": "missing", "payload": string(payloadBytes)},
		}).Result()
		if err != nil {
			slog.Error("Push to Redis stream failed", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}

		slog.Info("Successfully ingested webhook payload (Keyless)", "payload_len", len(payloadBytes), "provider", p.Name, "remote_addr", r.RemoteAddr)
		w.WriteHeader(http.StatusOK)
		return
	}

	lockKey := fmt.Sprintf("idempotency:%s", idempKey)
	ttlSeconds := 86400 // 24 Hours

	result, err := ingestScript.Run(ctx, srv.redisClient,
		[]string{lockKey, streamName}, // KEYS
		ttlSeconds, maxStreamLength, idempKey, string(payloadBytes),
	).Int()

	if err != nil {
		slog.Error("Redis script execution failed", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if result == 0 {
		slog.Info("Duplicate webhook dropped", "duplicate_key", idempKey)
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("Successfully ingested webhook payload", "payload_len", len(payloadBytes), "provider", p.Name, "remote_addr", r.RemoteAddr)
	w.WriteHeader(http.StatusOK)
}
