-include .env
export

.PHONY: run-bifrost
run-bifrost:
	go run cmd/bifrost-webhook-ingestor/main.go

.PHONY: bench
bench:
	wrk -t12 -c100 -d30s -s ./test/load/payload.lua http://127.0.0.1:8080
