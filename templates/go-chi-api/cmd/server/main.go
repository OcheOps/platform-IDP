package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/platform-idp/example-go-api/internal/app"
	"github.com/platform-idp/example-go-api/internal/safelog"
)

func main() {
	logger := safelog.NewJSON(os.Stdout, os.Getenv("SENSITIVE_LOG_FIELDS"))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var database *pgxpool.Pool
	var readinessDatabase interface {
		Ping(context.Context) error
	}
	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			logger.Error("database configuration failed")
			os.Exit(1)
		}
		database = pool
		readinessDatabase = pool
		defer database.Close()
	}

	server := &http.Server{
		Addr:              httpAddress(),
		Handler:           app.NewHandler(logger, readinessDatabase),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
		logger.Info("server stopped")
	}
}

func httpAddress() string {
	if address := os.Getenv("HTTP_ADDRESS"); address != "" {
		return address
	}
	return ":8080"
}
