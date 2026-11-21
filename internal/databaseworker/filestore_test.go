package databaseworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func TestFileStoreMovesDatabaseRequestToDeployment(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	record := map[string]any{
		"requestId": id, "serviceName": "orders-api", "requestState": "provisioning_database", "auditEvents": []any{},
		"resourceIdentities": map[string]string{
			"databaseName": "svc_orders_api_8c7a25e2", "databaseUser": "usr_orders_api_8c7a25e2",
			"databaseSecretName":         "platform/small-team-idp/orders-api/staging/database",
			"databasePasswordSecretName": "platform/small-team-idp/orders-api/staging/internal/database-password",
		},
	}
	data, _ := json.Marshal(record)
	path := filepath.Join(root, "requests", id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := databaseworker.NewFileStore(root, "shared.internal", 5432, "platform/small-team-idp/shared/postgres-master")
	work, err := store.NextDatabase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.DatabaseName != "svc_orders_api_8c7a25e2" || work.DatabaseUser != "usr_orders_api_8c7a25e2" || work.DatabaseHost != "shared.internal" || work.DatabasePort != 5432 {
		t.Fatalf("unexpected database work: %#v", work)
	}
	if err := store.Started(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := store.Ready(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"requestState": "deploying"`, `"databaseState": "ready"`, `"action": "database.provisioned"`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("missing %s in %s", want, stored)
		}
	}
}

func TestFileStoreReadyCannotBeRegressedByConcurrentFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	data, _ := json.Marshal(map[string]any{"requestId": id, "requestState": "provisioning_database", "databaseState": "provisioning", "auditEvents": []any{}})
	path := filepath.Join(root, "requests", id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := databaseworker.NewFileStore(root, "shared.internal", 5432, "master")
	var wait sync.WaitGroup
	wait.Add(2)
	go func() { defer wait.Done(); _ = store.Ready(context.Background(), id) }()
	go func() { defer wait.Done(); _ = store.Failed(context.Background(), id, "late_failure") }()
	wait.Wait()
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), `"requestState": "deploying"`) || !strings.Contains(string(stored), `"databaseState": "ready"`) {
		t.Fatalf("ready state regressed: %s", stored)
	}
}
