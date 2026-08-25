package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/OcheOps/platform-IDP/internal/bootstrap"
	"github.com/OcheOps/platform-IDP/internal/platformapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("platform API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := valueOrDefault("HTTP_ADDRESS", "127.0.0.1:8090")
	if err := requireLoopback(address); err != nil {
		return err
	}
	token := os.Getenv("PLATFORM_API_DEV_TOKEN")
	if len(token) < 32 {
		return errors.New("PLATFORM_API_DEV_TOKEN must contain at least 32 characters")
	}
	installation, err := bootstrap.ReadConfig(valueOrDefault("INSTALLATION_CONFIG", ".idp/config.json"))
	if err != nil {
		return fmt.Errorf("read installation configuration: %w", err)
	}
	handler, err := platformapi.New(platformapi.Config{
		SchemaDirectory:      valueOrDefault("SCHEMA_DIRECTORY", "contracts/schemas"),
		TemplateDirectory:    valueOrDefault("GO_TEMPLATE_DIRECTORY", "templates/go-chi-api"),
		GeneratedDirectory:   valueOrDefault("GENERATED_DIRECTORY", ".platform-data"),
		GitHubOrganization:   installation.GitHub.Organization,
		AWSRegion:            installation.AWS.Region,
		GitHubActionsRoleARN: os.Getenv("GITHUB_ACTIONS_ROLE_ARN"),
		Authenticate:         developmentAuthenticator(token),
		Authorize:            func(string, string) error { return nil },
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("platform API listening", "address", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func developmentAuthenticator(expectedToken string) func(*http.Request) (string, error) {
	expectedHash := sha256.Sum256([]byte(expectedToken))
	return func(request *http.Request) (string, error) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		providedHash := sha256.Sum256([]byte(provided))
		if provided == "" || subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
			return "", platformapi.ErrUnauthenticated
		}
		actor := strings.TrimSpace(request.Header.Get("X-Platform-Actor"))
		if actor == "" {
			return "", platformapi.ErrUnauthenticated
		}
		return actor, nil
	}
}

func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid HTTP_ADDRESS: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("development API must listen on a loopback address")
	}
	return nil
}

func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
