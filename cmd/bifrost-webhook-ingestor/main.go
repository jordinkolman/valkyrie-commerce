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

func main() {
  log.Println("Setting up connection to Redis message queue...")

  redisURLStr := os.Getenv("REDIS_URL")
  if redisURLStr == "" {
    log.Fatal("REDIS_URL environment variable missing")
  }
  client, err := queue.NewRedisClient(os.Getenv(redisURLStr))

  if err != nil {
    log.Fatal("Could not connect to Redis: ", err.Error())
  }
  srv := &Server{
    redisClient: client,
  }
  defer func() { _ = client.Close() }()
  log.Println("Connected to Redis!")

  mux := http.NewServeMux()
  mux.HandleFunc("/webhook", srv.ingestWebhookRequest)

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

func (srv *Server) ingestWebhookRequest(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodPost {
    http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    return
  }
  log.Printf("Received %s request from %s\n", r.Method, r.RemoteAddr)

  // Enforce max payload size to protect memory
  r.Body = http.MaxBytesReader(w, r.Body, maxPayloadSize)

  payloadBytes, err := io.ReadAll(r.Body)
  if err != nil {
    log.Printf("Error reading body: %v\n", err)
    http.Error(w, "Payload too large or malformed", http.StatusBadRequest)
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
  defer cancel()

  err = srv.redisClient.XAdd(ctx, &redis.XAddArgs{
    Stream: "incoming_webhooks",
    Values: map[string]interface{}{
      "payload": string(payloadBytes),
    },
  }).Err()
  if err != nil {
    log.Printf("Could not push payload to Redis: %s", err.Error())
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  log.Printf("Successfully ingested %d bytes from %s\n", len(payloadBytes), r.RemoteAddr)

  w.WriteHeader(http.StatusOK)
}
