package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jordinkolman/valkyrie-commerce/internal/queue"
	"github.com/redis/go-redis/v9"
	"github.com/tidwall/gjson"
)

// max 5MB payload size (well more than sufficient for webhooks)
const maxPayloadSize = 5 * 1024 * 1024

type Server struct {
  redisClient *redis.Client
}

type WebhookType string

const (
  Fat WebhookType = "fat"
  Thin WebhookType = "thin"
)

type Provider struct {
	Name string `json:"name"`
	IdempotencySource string `json:"idempotency_source"`
	IdempotencyKey string `json:"idempotency_key"`
	Type WebhookType `json:"type"`
}

// Lua Script to optimize idempotent write times into a single request
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

func loadProviders(filepath string) ([]Provider, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var providers []Provider
	if err = json.NewDecoder(file).Decode(&providers); err != nil {
		return nil, fmt.Errorf("failed to decode provider config: %w", err)
	}
	return providers, nil
}

func main() {
  log.Println("Setting up connection to Redis message queue...")

  redisURLStr := os.Getenv("REDIS_URL")
  if redisURLStr == "" {
    log.Fatal("REDIS_URL environment variable missing")
  }
  client, err := queue.NewRedisClient(redisURLStr)

  if err != nil {
    log.Fatal("Could not connect to Redis: ", err.Error())
  }
  srv := &Server{
    redisClient: client,
  }
  defer func() { _ = client.Close() }()
  log.Println("Connected to Redis!")

  configPath := os.Getenv("PROVIDER_CONFIG_PATH")
  if configPath == "" {
	  configPath = "config/providers.json"
  }

  providers, err := loadProviders(configPath)
  if err != nil {
	  log.Fatalf("Fatal: Could not load provider configurations: %v", err)
  }
  log.Printf("Loaded %d webhook providers from config", len(providers))

  mux := http.NewServeMux()
  for _, provider := range providers {
	  route := fmt.Sprintf("POST /webhook/%s", provider.Name)
	  mux.HandleFunc(route, srv.buildWebhookHandler(provider))
  }

  portStr := os.Getenv("PORT")
  if portStr == "" {
    log.Fatal("PORT environment variable missing")
  }

  port := fmt.Sprintf(":%s", portStr)
  httpServer := &http.Server{
    Addr:              port,
    Handler:           mux,
    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       15 * time.Second,
    WriteTimeout:      15 * time.Second,
    IdleTimeout:       60 * time.Second,
  }
  
  // channel for receiving errors from server crash
  serverErr := make(chan error, 1)
  go func() {
    log.Printf("Summoning Bifrost on port %s...\n", port)
    if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
      serverErr <- err
    }
  }()

  // Channel to receive server terminate request
  quit := make(chan os.Signal, 1)
  signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

  select {
  case err := <- serverErr:
    log.Printf("Server crashed: %v", err)
  case <-quit:
    log.Println("Kill signal received. Shutting down gracefully...")
  }
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()

  if err := httpServer.Shutdown(ctx); err != nil {
    log.Fatalf("Server forced to shutdown abruptly: %v", err)
  }

  log.Println("Bifrost exited properly.")
}

func (srv *Server) buildWebhookHandler(p Provider) http.HandlerFunc {
  return func(w http.ResponseWriter, r *http.Request) {
    log.Printf("Received %s webhook from %s (%s)\n", p.Type, p.Name, r.RemoteAddr)
    streamName := "incoming_webhooks"
    if p.Type == Thin {
      streamName = "thin_webhooks"
    }

    srv.ingestToStream(w, r, streamName, p)
  }
}

func (srv *Server) ingestToStream(w http.ResponseWriter, r *http.Request, streamName string, p Provider) {
  // Enforce max payload size to protect memory
  r.Body = http.MaxBytesReader(w, r.Body, maxPayloadSize)

  payloadBytes, err := io.ReadAll(r.Body)
  if err != nil {
    log.Printf("Error reading body: %v\n", err)
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
	  log.Printf("Warning: Missing idempotency key for %s", p.Name)
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
      log.Printf("Push to Redis stream failed: %v", err)
      http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    }

	log.Printf("Successfully ingested %d bytes (Keyless) from %s (%s)\n", len(payloadBytes), p.Name, r.RemoteAddr)
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
    log.Printf("Redis script execution failed: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  if result == 0 {
    log.Printf("Duplicate webhook dropped: %s", idempKey)
    w.WriteHeader(http.StatusOK)
    return
  }

  log.Printf("Successfully ingested %d bytes from %s\n", len(payloadBytes), r.RemoteAddr)
  w.WriteHeader(http.StatusOK)
}
