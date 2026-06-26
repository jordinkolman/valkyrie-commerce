package main

import (
	"context"
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
  Name string
  IdempotencyHeader string
  Type WebhookType
}

var SupportedProviders = []Provider{
  {Name: "shopify", IdempotencyHeader: "X-Shopify-Webhook-Id", Type: Fat},
  {Name: "woocommerce", IdempotencyHeader: "X-WC-Webhook-ID", Type: Fat},
  {Name: "stripe", IdempotencyHeader: "Stripe-Signature", Type: Thin},
  {Name: "amazon", IdempotencyHeader: "x-amzn-RequestId", Type: Thin},
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

  mux := http.NewServeMux()

  for _, provider := range SupportedProviders {
    route := fmt.Sprintf("POST /webhook/%s", provider.Name)
    // handler wrapped in Closure to allow persistance of provider identity 
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
    log.Printf("Receved %s webhook from %s (%s)\n", p.Type, p.Name, r.RemoteAddr)

    idempKey := r.Header.Get(p.IdempotencyHeader)
    if idempKey == "" {
      log.Printf("Warning: Missing idempotency key for %s", p.Name)
      idempKey = "unknown"
    }

    streamName := "incoming_webhooks"
    if p.Type == Thin {
      streamName = "thin_webhooks"
    }

    srv.ingestToStream(w, r, streamName, idempKey)
  }
}

func (srv *Server) ingestToStream(w http.ResponseWriter, r *http.Request, streamName, idempKey string) {
  // Enforce max payload size to protect memory
  r.Body = http.MaxBytesReader(w, r.Body, maxPayloadSize)

  payloadBytes, err := io.ReadAll(r.Body)
  if err != nil {
    log.Printf("Error reading body: %v\n", err)
    http.Error(w, "Payload too large or malformed", http.StatusBadRequest)
    return
  }

  ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
  defer cancel()

  maxStreamLength := 10000

  if idempKey == "unknown" || idempKey == "" {
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
