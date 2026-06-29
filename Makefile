-include .env
export

.PHONY: up
up:
	docker-compose -f deploy/docker-compose.yml up -d 

.PHONY: down
down:
	docker-compose -f deploy/docker-compose.yml down

.PHONY: run-bifrost
run-bifrost:
	go run cmd/bifrost/main.go

.PHONY: bench
bench:
	wrk -t12 -c100 -d30s -s ./test/load/payload.lua http://127.0.0.1:8080
