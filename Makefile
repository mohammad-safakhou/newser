SHELL := /bin/bash

.PHONY: up down logs fmt build test tidy serve cli migrate webui-install webui-build webui-clean serve-all swagger-install swagger swagger-clean

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

run:
	go run ./

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

# -----------------------------
# Swagger docs generation
# -----------------------------
swagger-install:
	GO111MODULE=on go install github.com/swaggo/swag/cmd/swag@latest

# Generate swagger docs from annotations
swagger:
	swag init -o ./internal/server/swagger --parseDependency --parseInternal
	swag fmt -d ./internal/server/swagger

swagger-clean:
	rm -rf ./internal/server/swagger
	mkdir -p ./internal/server/swagger
	echo "package swagger" > ./internal/server/swagger/doc.go
