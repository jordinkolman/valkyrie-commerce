package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	Fat  WebhookType = "fat"
	Thin WebhookType = "thin"
)

type Provider struct {
	Name              string      `json:"name"`
	IdempotencySource string      `json:"idempotency_source"`
	IdempotencyKey    string      `json:"idempotency_key"`
	Type              WebhookType `json:"type"`
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

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	slog.Info("Setting up connection to Redis message queue...")

	redisURLStr := os.Getenv("REDIS_URL")
	if redisURLStr == "" {
		slog.Error("REDIS_URL environment variable missing")
		os.Exit(1)
	}
	client, err := queue.NewRedisClient(redisURLStr)

	if err != nil {
		slog.Error("Could not connect to Redis", "error", err.Error())
		os.Exit(1)
	}
	srv := &Server{
		redisClient: client,
	}
	defer func() { _ = client.Close() }()
	slog.Info("Connected to Redis!")

	configPath := os.Getenv("PROVIDER_CONFIG_PATH")
	if configPath == "" {
		exePath, err := os.Executable()
		if err != nil {
			slog.Error("Fatal: Could not resolve executable path", "error", err)
			os.Exit(1)
		}
		configPath = filepath.Join(filepath.Dir(exePath), "config", "providers.json")
	}

	providers, err := loadProviders(configPath)
	if err != nil {
		slog.Error("Fatal: Could not load provider configurations", "error", err)
		os.Exit(1)
	}
	slog.Info("Loaded webhook providers from config", "count", len(providers))

	mux := http.NewServeMux()
	for _, provider := range providers {
		route := fmt.Sprintf("POST /webhook/%s", provider.Name)
		mux.HandleFunc(route, srv.buildWebhookHandler(provider))
	}

	portStr := os.Getenv("PORT")
	if portStr == "" {
		slog.Error("PORT environment variable missing")
		os.Exit(1)
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
		slog.Info("Summoning Bifrost", "port", port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Channel to receive server terminate request
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("Server crashed", "error", err)
		os.Exit(1)
	case <-quit:
		slog.Info("Kill signal received. Shutting down gracefully")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown abruptly", "error", err)
		os.Exit(1)
	}

	slog.Info("Bifrost exited properly.")
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
