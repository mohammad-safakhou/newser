# syntax=docker/dockerfile:1
FROM golang:1.24 as gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newserd ./cmd/newserd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newser ./cmd/newser

# Build WebUI with Node (optional). If webui exists, build it.
FROM node:20-alpine as uibuild
WORKDIR /ui
COPY webui/package.json webui/package-lock.json* ./
RUN [ -f package.json ] && npm ci || true
COPY webui .
RUN [ -f package.json ] && npm run build || true

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=gobuild /out/newserd /usr/local/bin/newserd
COPY --from=gobuild /out/newser /usr/local/bin/newser
COPY docs /app/docs
COPY migrations /app/migrations
# If UI was built, copy dist to serve statically
COPY --from=uibuild /ui/dist /app/webui/dist
ENV NEWSER_HTTP_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/newserd"]

