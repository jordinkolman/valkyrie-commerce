# Bifrost: Webhook Ingestion Gateway

Bifrost is an edge gateway optimized strictly for speed and memory safety. It acts as the frontline receiver for all third-party provider webhooks (Shopify, Stripe, etc.).


## Technical Design

Bifrost achieves >16,000 requests per second by completely bypassing standard application-layer bottlenecks:

1. **Zero-Allocation Routing:** Bifrost strictly refuses to unmarshal JSON payloads into Go structs. Idempotency keys are extracted from HTTP headers or parsed directly from raw byte slices using `gjson` to prevent memory allocation overhead.
2. **Atomic Idempotency:** Checking for duplicate events and writing to the queue are collapsed into a single, atomic network hop to the Redis cache via a custom Lua script, eliminating distributed race conditions.
3. **OOM Protection:** Incoming connections are bounded using `http.MaxBytesReader` to instantly reject oversized or malicious payloads before reading them into memory.
4. **Detached Contexts:** In-flight writes to the Redis message queue utilize `context.WithoutCancel` to guarantee message ingestion completes even if the client terminates the HTTP connection prematurely.

## Configuration
Webhook routing is dynamically controlled via `config/providers.json`. Bifrost supports both `header` and `payload` idempotency extraction depending on the provider's specification.
