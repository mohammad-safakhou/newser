package runtime

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// RunPlaceholder logs service startup and blocks until shutdown signals arrive.
// It provides a safe scaffold until each service gains a concrete implementation.
func RunPlaceholder(ctx context.Context, service string) error {
	if service == "" {
		service = "service"
	}
	log.Printf("[%s] placeholder service running — waiting for implementation", service)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
		log.Printf("[%s] context cancelled, shutting down", service)
	case sig := <-sigCh:
		log.Printf("[%s] received signal %s, shutting down", service, sig)
	}
	return nil
}
