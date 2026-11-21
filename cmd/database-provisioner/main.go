package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("database provisioning failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database provisioning completed")
}

func run(ctx context.Context, getenv func(string) string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	work, region, err := workFromEnvironment(getenv)
	if err != nil {
		return err
	}
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return errors.New("load AWS workload identity")
	}
	store := &singleWorkStore{work: work}
	secretStore := databaseworker.NewSecretsManager(secretsmanager.NewFromConfig(awsConfig), databaseworker.CryptoSecretPasswordGenerator{})
	if err := databaseworker.New(store, secretStore, databaseworker.PGXConnector{}).Reconcile(ctx); err != nil {
		return err
	}
	return nil
}

func workFromEnvironment(getenv func(string) string) (databaseworker.Work, string, error) {
	port, err := strconv.Atoi(strings.TrimSpace(getenv("DATABASE_PORT")))
	if err != nil {
		return databaseworker.Work{}, "", errors.New("DATABASE_PORT must be an integer")
	}
	work := databaseworker.Work{
		RequestID: strings.TrimSpace(getenv("REQUEST_ID")), DatabaseName: strings.TrimSpace(getenv("DATABASE_NAME")),
		DatabaseUser: strings.TrimSpace(getenv("DATABASE_USER")), DatabaseHost: strings.TrimSpace(getenv("DATABASE_HOST")), DatabasePort: port,
		MasterSecretName: strings.TrimSpace(getenv("MASTER_SECRET_NAME")), PasswordSecretName: strings.TrimSpace(getenv("PASSWORD_SECRET_NAME")),
		SecretName: strings.TrimSpace(getenv("SERVICE_SECRET_NAME")),
	}
	region := strings.TrimSpace(getenv("AWS_REGION"))
	if region == "" {
		return databaseworker.Work{}, "", errors.New("AWS_REGION is required")
	}
	if work.RequestID == "" || work.DatabaseName == "" || work.DatabaseUser == "" || work.DatabaseHost == "" || work.MasterSecretName == "" || work.PasswordSecretName == "" || work.SecretName == "" {
		return databaseworker.Work{}, "", errors.New("database provisioner environment is incomplete")
	}
	return work, region, nil
}

type singleWorkStore struct{ work databaseworker.Work }

func (store *singleWorkStore) NextDatabase(context.Context) (databaseworker.Work, error) {
	return store.work, nil
}
func (store *singleWorkStore) Started(context.Context, string) error { return nil }
func (store *singleWorkStore) Ready(context.Context, string) error   { return nil }
func (store *singleWorkStore) Failed(_ context.Context, _ string, code string) error {
	return fmt.Errorf("database provisioning stopped with code %s", code)
}
