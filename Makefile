-include .env
export

.PHONY: run-bifrost

run-bifrost:
	go run cmd/bifrost-webhook-ingestor/main.go
