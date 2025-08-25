# syntax=docker/dockerfile:1
FROM golang:1.24 as gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newserd ./cmd/newserd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newser ./cmd/newser

# Build WebUI with Node (optional). If webui exists, build it.
## UI build removed; frontend is a separate service

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=gobuild /out/newserd /usr/local/bin/newserd
COPY --from=gobuild /out/newser /usr/local/bin/newser
COPY docs /app/docs
COPY migrations /app/migrations
# Copy built WebUI to the dist path expected by the server
# no UI assets copied; served by separate webui container
ENV NEWSER_HTTP_ADDR=:10001
EXPOSE 10001
ENTRYPOINT ["/usr/local/bin/newserd"]

