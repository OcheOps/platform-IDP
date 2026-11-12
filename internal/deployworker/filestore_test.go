package deployworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/deployworker"
)

func TestFileStoreReadsAppliedDatabaseSecretIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"requestId":    "request-1",
		"serviceName":  "orders-api",
		"requestState": "deploying",
		"request": map[string]any{
			"metadata": map[string]any{"owner": "orders-team"},
			"spec": map[string]any{
				"template": map[string]any{"id": "go-chi-api", "version": "v0.1.0"},
				"capacity": map[string]any{"cpu": "250m", "memory": "256Mi", "minReplicas": 1, "maxReplicas": 3},
				"database": map[string]any{"engine": "postgres"},
			},
		},
		"resourceIdentities": map[string]string{
			"databaseSecretName": "platform/small-team-idp/orders-api/staging/database",
			"secretStoreRoleArn": "arn:aws:iam::123456789012:role/small-team-idp-orders-api-staging-secrets-8c7a25e2",
		},
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "requests", "request-1.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := deployworker.NewFileStore(root, "acme", "platform-config", "example.com", "123456789012", "eu-west-1")
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.DatabaseSecretName != "platform/small-team-idp/orders-api/staging/database" || work.SecretStoreRoleARN != "arn:aws:iam::123456789012:role/small-team-idp-orders-api-staging-secrets-8c7a25e2" {
		t.Fatalf("applied resource identities were not propagated: %#v", work)
	}
}
