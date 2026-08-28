package applyworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/applyworker"
)

func TestFileStorePersistsAppliedResourceIdentities(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	digest := "sha256:" + strings.Repeat("a", 64)
	record := map[string]any{
		"requestId": id, "serviceName": "orders-api", "requestState": "approved", "configHeadSHA": "head-sha",
		"configPullRequestNumber": 17, "planDigest": digest, "request": map[string]any{"spec": map[string]any{"database": map[string]string{"engine": "postgres"}}}, "auditEvents": []any{},
	}
	data, _ := json.Marshal(record)
	path := filepath.Join(root, "requests", id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := applyworker.NewFileStore(root, "acme", "platform-config", "small-team-idp", "123456789012", "eu-west-1")
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !work.Database || work.PlanDigest != digest {
		t.Fatalf("unexpected work: %#v", work)
	}
	if err := store.Applied(context.Background(), work, "merge-sha"); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"requestState": "deploying"`, `"applyMergeSHA": "merge-sha"`, `"action": "apply.completed"`, `"applyOperationKey": "` + id + `/apply/head-sha"`, `"canonicalId": "small-team-idp/orders-api/staging"`, `"ecrRepository": "platform/orders-api"`, `"databaseSecretName": "platform/small-team-idp/orders-api/staging/database"`, `"planDigest": "` + digest + `"`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("missing %s", want)
		}
	}
}
