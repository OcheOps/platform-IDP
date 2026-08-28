package configworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/configworker"
)

func TestFileStoreSelectsRepositoryReadyRequestAndPersistsPlanningIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	record := map[string]any{"requestId": id, "serviceName": "orders-api", "requestState": "draft", "repositoryState": "repository_ready", "request": map[string]any{"kind": "ServiceRequest"}, "costEstimate": map[string]any{"digest": "sha256:abc"}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(root, "requests", id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := configworker.NewFileStore(root, "acme", "platform-config", configworker.Platform{})
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.Branch != "platform/request-"+id || work.Repository != "platform-config" {
		t.Fatalf("unexpected work: %#v", work)
	}
	if err := store.Started(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPullRequest(context.Background(), id, 17, "head-sha"); err != nil {
		t.Fatal(err)
	}
	if err := store.Planning(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(root, "requests", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"requestState": "planning"`, `"configState": "pull_request_open"`, `"configPullRequestNumber": 17`, `"configHeadSHA": "head-sha"`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("missing %s", want)
		}
	}
}
