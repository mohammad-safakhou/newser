# syntax=docker/dockerfile:1
FROM golang:1.24 AS gobuild
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/executor ./cmd/executor
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/crawler ./cmd/crawler
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/memory ./cmd/memory
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/schema ./cmd/schema
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/tools ./cmd/tools
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/newser ./

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=gobuild /out/api /usr/local/bin/api
COPY --from=gobuild /out/worker /usr/local/bin/worker
COPY --from=gobuild /out/executor /usr/local/bin/executor
COPY --from=gobuild /out/crawler /usr/local/bin/crawler
COPY --from=gobuild /out/memory /usr/local/bin/memory
COPY --from=gobuild /out/schema /usr/local/bin/schema
COPY --from=gobuild /out/tools /usr/local/bin/tools
COPY --from=gobuild /out/newser /usr/local/bin/newser
COPY --from=gobuild /src/config /app/config
COPY migrations /app/migrations
ENV NEWSER_HTTP_ADDR=:10001
EXPOSE 10001
ENTRYPOINT ["/usr/local/bin/newser"]
