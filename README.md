# Valkyrie Commerce

An event-driven, microservice-based B2B e-commerce infrastructure platform built in Go.

This repository uses a monorepo structure to house multiple decoupled services that communicate asynchronously via Redis Streams, designed for high throughput and fault tolerance.

## 🏗 Architecture

* **Bifrost (Edge Ingestor):** A high-throughput, synchronous HTTP gateway that receives incoming webhooks, validates them, and pushes raw payloads to a message queue.
* **Mimir (Normalizer):** *(In Development)* A concurrent worker pool that consumes raw events, normalizes payloads into strict domain models, and writes to the core ledger.
* **Heimdall (Auth Gateway):** *(Planned)*


## 🚀 Services


### 1. Bifrost (Webhook Ingestor)
Bifrost is designed exclusively for speed and memory safety. It performs no JSON parsing or database lookups. It utilizes `http.MaxBytesReader` to strictly cap memory allocation (preventing OOM panics from malicious payloads) and drops valid requests directly into a Redis Stream (`incoming_webhooks`) for asynchronous processing.


#### Prerequisites
* Go 1.21+
* Docker & Docker Compose
* `make` utility

#### Running Locally

**1. Boot the Message Queue**
Start the Redis Stack (includes RedisInsight on port 8001):
```bash
docker-compose up -d
```

**2. Configure the Environment**

Create a `.env` file at the root of the project:
```text
PORT=8080
REDIS_URL=localhost:6379
```


**3. Start the Service**
The project uses a Makefile to automatically inject environment variables before booting the Go binary.

```bash
make run-bifrost

```

#### Testing

Send a raw POST request to the local endpoint:

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{"event": "order_created", "id": 12345}'
```
