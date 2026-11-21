package databaseworker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func TestWorkerProvisionsDatabaseAndPublishesOnlyDatabaseURL(t *testing.T) {
	store := &fakeStore{work: databaseworker.Work{
		RequestID: "request-1", DatabaseName: "svc_orders_api_8c7a25e2", DatabaseUser: "usr_orders_api_8c7a25e2",
		DatabaseHost: "shared.example.internal", DatabasePort: 5432, MasterSecretName: "platform/small-team-idp/shared/postgres-master",
		PasswordSecretName: "platform/small-team-idp/orders-api/staging/internal/database-password",
		SecretName:         "platform/small-team-idp/orders-api/staging/database",
	}}
	secrets := &fakeSecrets{master: databaseworker.MasterCredentials{Username: "platform_admin", Password: "master-secret"}, password: "generated/p@ss"}
	database := &fakeDatabase{}
	worker := databaseworker.New(store, secrets, database)
	if err := worker.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if database.input.Name != store.work.DatabaseName || database.input.User != store.work.DatabaseUser || database.input.Password != "generated/p@ss" {
		t.Fatalf("unexpected database input: %#v", database.input)
	}
	if secrets.passwordName != store.work.PasswordSecretName || secrets.name != store.work.SecretName || len(secrets.value) != 1 {
		t.Fatalf("unexpected secret publication: name=%q value=%#v", secrets.name, secrets.value)
	}
	want := "postgres://usr_orders_api_8c7a25e2:generated%2Fp%40ss@shared.example.internal:5432/svc_orders_api_8c7a25e2?sslmode=require"
	if secrets.value["DATABASE_URL"] != want {
		t.Fatalf("DATABASE_URL = %q, want %q", secrets.value["DATABASE_URL"], want)
	}
	if strings.Contains(secrets.value["DATABASE_URL"], "master-secret") || store.state != "ready" {
		t.Fatalf("master secret leaked or work not ready: %#v %#v", secrets.value, store)
	}
}

type fakeStore struct {
	work  databaseworker.Work
	state string
}

func (store *fakeStore) NextDatabase(context.Context) (databaseworker.Work, error) {
	return store.work, nil
}
func (store *fakeStore) Started(context.Context, string) error { store.state = "started"; return nil }
func (store *fakeStore) Ready(context.Context, string) error   { store.state = "ready"; return nil }
func (store *fakeStore) Failed(context.Context, string, string) error {
	store.state = "failed"
	return nil
}

type fakeSecrets struct {
	master       databaseworker.MasterCredentials
	password     string
	passwordName string
	name         string
	value        map[string]string
}

func (secrets *fakeSecrets) MasterCredentials(context.Context, string) (databaseworker.MasterCredentials, error) {
	return secrets.master, nil
}
func (secrets *fakeSecrets) PrepareServicePassword(_ context.Context, name string) (string, error) {
	secrets.passwordName = name
	return secrets.password, nil
}
func (secrets *fakeSecrets) PutServiceSecret(_ context.Context, name string, value map[string]string) error {
	secrets.name, secrets.value = name, value
	return nil
}

type fakeDatabase struct{ input databaseworker.Database }

func (database *fakeDatabase) Connect(_ context.Context, credentials databaseworker.MasterCredentials, host string, port int) (databaseworker.Provisioner, error) {
	if credentials.Username != "platform_admin" || credentials.Password != "master-secret" || host != "shared.example.internal" || port != 5432 {
		panic("unexpected admin connection")
	}
	return database, nil
}
func (database *fakeDatabase) Ensure(_ context.Context, input databaseworker.Database) error {
	database.input = input
	return nil
}
