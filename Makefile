SHELL := /bin/bash

.PHONY: up down logs fmt build test tidy serve

up:
	docker compose up -d

down:
	docker compose down -v

logs:
	docker compose logs -f --tail=200

fmt:
	go fmt ./...

tidy:
	go mod tidy

build:
	go build ./...

test:
	go test ./...

serve:
	go run ./cmd/newserd

 