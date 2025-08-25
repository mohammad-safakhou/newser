package main

import (
	"log"
	"os"

	"github.com/mohammad-safakhou/newser/internal/server"
)

func main() {
	addr := os.Getenv("NEWSER_HTTP_ADDR")
	if addr == "" {
		addr = ":10001"
	}

	if err := server.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}


