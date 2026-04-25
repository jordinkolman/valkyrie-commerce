package main

import (
	"io"
	"log"
	"net/http"
)

// max 5MB payload size (well more than sufficient for webhooks)
const maxPayloadSize = 5 * 1024 * 1024

func main() {
  mux := http.NewServeMux()
  mux.HandleFunc("/webhook", ingestWebhookRequest)

  log.Println("Summoning Bifrost on port 8080...")
  err := http.ListenAndServe(":8080", mux)
  if err != nil {
    log.Fatal(err)
  }
}

func ingestWebhookRequest(w http.ResponseWriter, r *http.Request) {
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

  log.Printf("Successfully ingested %d bytes from %s\n", len(payloadBytes), r.RemoteAddr)

  w.WriteHeader(http.StatusOK)
}
