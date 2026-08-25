package repositoryworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

func TestFileStorePersistsRepositoryIdentityAndReadyState(t *testing.T) {
	root := t.TempDir()
	requestID := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	requestDirectory := filepath.Join(root, "requests")
	if err := os.MkdirAll(requestDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"requestId": requestID, "serviceName": "orders-api", "repositoryState": "repository_creating", "requestState": "draft",
		"request": map[string]any{"metadata": map[string]any{"owner": "orders-team"}}, "auditEvents": []any{},
	}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(requestDirectory, requestID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := repositoryworker.NewFileStore(root, "acme")
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.Name != "orders-api" || work.OwnerTeam != "orders-team" || work.Organization != "acme" || work.RenderedPath != filepath.Join(root, "repositories", "orders-api") {
		t.Fatalf("unexpected work: %#v", work)
	}
	if err := store.Started(context.Background(), requestID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRepository(context.Background(), requestID, 42); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Next(context.Background())
	if err != nil || recovered.RepositoryID != 42 {
		t.Fatalf("work was stranded after identity persistence: %#v, %v", recovered, err)
	}
	if err := store.Ready(context.Background(), requestID); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(requestDirectory, requestID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"githubRepositoryId": 42`, `"repositoryState": "repository_ready"`, `"action": "repository.started"`, `"action": "repository.created"`} {
		if !containsJSONText(stored, expected) {
			t.Errorf("record missing %s: %s", expected, stored)
		}
	}
}

func containsJSONText(data []byte, expected string) bool {
	var compact any
	if json.Unmarshal(data, &compact) != nil {
		return false
	}
	pretty, _ := json.MarshalIndent(compact, "", "  ")
	return stringContains(string(pretty), expected)
}

func stringContains(value, expected string) bool {
	for i := 0; i+len(expected) <= len(value); i++ {
		if value[i:i+len(expected)] == expected {
			return true
		}
	}
	return false
}
