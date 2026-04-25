package main

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/redis/go-redis/v9"
  "github.com/jordinkolman/valkyrie-commerce/internal/queue"
)

// max 5MB payload size (well more than sufficient for webhooks)
const maxPayloadSize = 5 * 1024 * 1024

type Server struct {
  redisClient *redis.Client
}

func main() {
  log.Println("Setting up connection to Redis message queue...")

  client, err := queue.NewRedisClient("localhost:6379")
  if err != nil {
    log.Fatal("Could not connect to Redis: ", err.Error())
  }
  srv := &Server{
    redisClient: client,
  }
  defer client.Close()
  log.Println("Connected to Redis!")

  mux := http.NewServeMux()
  mux.HandleFunc("/webhook", srv.ingestWebhookRequest)

  log.Println("Summoning Bifrost on port 8080...")
  err = http.ListenAndServe(":8080", mux)
  if err != nil {
    log.Fatal(err)
  }
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

  ctx := context.Background()
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
