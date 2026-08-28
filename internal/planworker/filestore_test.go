package planworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/planworker"
)

func TestFileStorePersistsPlanEvidenceAndAuditEvent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	record := map[string]any{"requestId": id, "requestState": "planning", "configState": "pull_request_open", "configHeadSHA": "head-sha", "configPullRequestNumber": 17, "costEstimate": map[string]string{"digest": "sha256:" + strings.Repeat("b", 64)}, "auditEvents": []any{}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(root, "requests", id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := planworker.NewFileStore(root, "acme", "platform-config")
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.HeadSHA != "head-sha" || work.PullRequestNumber != 17 {
		t.Fatalf("unexpected work: %#v", work)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := store.AwaitingApproval(context.Background(), id, digest); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(root, "requests", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"requestState": "awaiting_approval"`, `"planDigest": "` + digest + `"`, `"action": "plan.completed"`, `"headSHA": "head-sha"`} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("missing %s", want)
		}
	}
}
