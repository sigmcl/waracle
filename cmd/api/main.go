package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sigmcl/waracle/internal/hotel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	databaseURL := env("DATABASE_URL", "postgres://waracle:waracle@localhost:5432/waracle?sslmode=disable")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := hotel.OpenStore(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	notifier := hotel.NewNotifier(2*time.Second, func(reference string) {
		logger.Info("confirmation email simulated", "booking_reference", reference)
	})
	defer notifier.Close()

	server := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           hotel.NewHandler(store, notifier, logger),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
