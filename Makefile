SHELL := /bin/bash

.PHONY: up down logs fmt build test tidy serve cli migrate webui-install webui-build webui-clean serve-all

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
	go test ./... -run . -count=1

serve:
	go run ./cmd/newserd

cli:
	go run ./cmd/newser

migrate:
	go run ./cmd/newser migrate --direction up

webui-install:
	cd webui && npm ci

webui-build: webui-install
	cd webui && npm run build

webui-clean:
	rm -rf webui/node_modules webui/dist

serve-all: webui-build
	go run ./cmd/newserd

webui-dev:
	cd webui && npm run dev
