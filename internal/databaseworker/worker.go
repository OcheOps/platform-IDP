package databaseworker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
)

var ErrNoWork = errors.New("no database provisioning work is ready")

type Work struct {
	// ServiceName is used only to name control-plane Jobs; the in-cluster
	// provisioner does not require it.
	ServiceName                                                                                           string
	RequestID, DatabaseName, DatabaseUser, DatabaseHost, MasterSecretName, PasswordSecretName, SecretName string
	DatabasePort                                                                                          int
}

type MasterCredentials struct {
	Username, Password string
}

type Database struct {
	Name, User, Password string
}

type Store interface {
	NextDatabase(context.Context) (Work, error)
	Started(context.Context, string) error
	Ready(context.Context, string) error
	Failed(context.Context, string, string) error
}

type Secrets interface {
	MasterCredentials(context.Context, string) (MasterCredentials, error)
	// PrepareServicePassword atomically returns the existing password or creates
	// one. Concurrent callers for a secret name must receive the same value.
	PrepareServicePassword(context.Context, string) (string, error)
	PutServiceSecret(context.Context, string, map[string]string) error
}

type Provisioner interface {
	Ensure(context.Context, Database) error
}

type DatabaseConnector interface {
	Connect(context.Context, MasterCredentials, string, int) (Provisioner, error)
}

type Worker struct {
	store    Store
	secrets  Secrets
	database DatabaseConnector
}

func New(store Store, secrets Secrets, database DatabaseConnector) *Worker {
	return &Worker{store: store, secrets: secrets, database: database}
}

func (worker *Worker) Reconcile(ctx context.Context) error {
	work, err := worker.store.NextDatabase(ctx)
	if err != nil {
		return err
	}
	if err := validateWork(work); err != nil {
		return worker.fail(ctx, work.RequestID, "database_work_invalid", err)
	}
	if err := worker.store.Started(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record database provisioning start: %w", err)
	}
	master, err := worker.secrets.MasterCredentials(ctx, work.MasterSecretName)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "database_master_secret_unavailable", errors.New("read database master credentials"))
	}
	if master.Username == "" || master.Password == "" {
		return worker.fail(ctx, work.RequestID, "database_master_secret_invalid", errors.New("database master credentials are incomplete"))
	}
	password, err := worker.secrets.PrepareServicePassword(ctx, work.PasswordSecretName)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "database_password_preparation_failed", errors.New("prepare service database password"))
	}
	if password == "" {
		return worker.fail(ctx, work.RequestID, "database_password_invalid", errors.New("service database password is empty"))
	}
	provisioner, err := worker.database.Connect(ctx, master, work.DatabaseHost, work.DatabasePort)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "database_connection_failed", errors.New("connect to shared database"))
	}
	if err := provisioner.Ensure(ctx, Database{Name: work.DatabaseName, User: work.DatabaseUser, Password: password}); err != nil {
		return worker.fail(ctx, work.RequestID, "database_provision_failed", errors.New("provision service database"))
	}
	databaseURL := &url.URL{
		Scheme: "postgres", User: url.UserPassword(work.DatabaseUser, password),
		Host: net.JoinHostPort(work.DatabaseHost, strconv.Itoa(work.DatabasePort)), Path: "/" + work.DatabaseName,
		RawQuery: "sslmode=require",
	}
	if err := worker.secrets.PutServiceSecret(ctx, work.SecretName, map[string]string{"DATABASE_URL": databaseURL.String()}); err != nil {
		return worker.fail(ctx, work.RequestID, "database_secret_write_failed", errors.New("write service database secret"))
	}
	if err := worker.store.Ready(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record database readiness: %w", err)
	}
	return nil
}

func validateWork(work Work) error {
	if work.RequestID == "" || work.DatabaseName == "" || work.DatabaseUser == "" || work.DatabaseHost == "" || work.MasterSecretName == "" || work.PasswordSecretName == "" || work.SecretName == "" || work.PasswordSecretName == work.SecretName || work.DatabasePort < 1 || work.DatabasePort > 65535 {
		return errors.New("database work is incomplete")
	}
	return nil
}

func (worker *Worker) fail(ctx context.Context, requestID, code string, cause error) error {
	if err := worker.store.Failed(ctx, requestID, code); err != nil {
		return errors.Join(cause, fmt.Errorf("record database failure: %w", err))
	}
	return cause
}
