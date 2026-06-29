# Valkyrie Commerce Infrastructure

An event-driven, microservice-based B2B e-commerce infrastructure platform built in Go. 

This repository utilizes an idiomatic Go monorepo architecture (`cmd/`, `internal/`, `pkg/`) to house decoupled services that communicate asynchronously via Redis Streams. It is designed for high throughput, memory safety, and fault-tolerant event ingestion.

## Architecture

* **Bifrost (Edge Gateway):** A high-throughput, synchronous HTTP gateway that receives incoming webhooks, protects memory bounds, enforces deduplication atomically via Lua, and pushes raw byte arrays to a Redis message queue.

* **Mimir (Consumer Pool):** *(In Development)* A concurrent background worker pool that consumes raw events from Redis via consumer groups, normalizes payloads, and persists them to the relational ledger.

## Repository Structure

* `cmd/`: Entry points for microservice binaries.
* `internal/`: Private domain logic, HTTP handlers, and configuration parsers.
* `deploy/`: Container orchestration and infrastructure templates.
* `config/`: JSON definitions for webhook provider routing.
* `test/load/`: Multi-threaded `wrk` Lua scripts for local benchmarking.

## Local Development


**1. Boot the Message Broker**

Start the Redis Stack (includes RedisInsight on port 8001):
```bash

make up
```


**2. Start Bifrost**

```bash
make run-bifrost
```

**3. Run the Test Suite**
```bash
go test -v -race ./internal/...
```
