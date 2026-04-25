include .env
export

run-bifrost:
	go run cmd/bifrost-webhook-ingestor/main.go
