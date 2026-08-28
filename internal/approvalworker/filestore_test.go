package approvalworker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/approvalworker"
)

func TestFileStorePersistsIndependentApprovalEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "requests"), 0o750); err != nil {
		t.Fatal(err)
	}
	id := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	digest := "sha256:" + strings.Repeat("a", 64)
	record := map[string]any{
		"requestId": id, "requestState": "awaiting_approval", "actor": "requester",
		"configHeadSHA": "head-sha", "configPullRequestNumber": 17, "planDigest": digest,
		"auditEvents": []any{},
	}
	data, _ := json.Marshal(record)
	path := filepath.Join(root, "requests", id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := approvalworker.NewFileStore(root, "acme", "platform-config", "engineering-owners", "platform-owners")
	work, err := store.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if work.Requester != "requester" || work.HeadSHA != "head-sha" || work.PlanDigest != digest {
		t.Fatalf("unexpected work: %#v", work)
	}
	if err := store.Approved(context.Background(), id, approvalworker.Evidence{EngineeringApprover: "alice", PlatformApprover: "bob"}); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"requestState": "approved"`, `"engineeringApprover": "alice"`, `"platformApprover": "bob"`,
		`"action": "approval.granted"`, `"role": "engineering_owner"`, `"role": "platform_owner"`,
		`"headSHA": "head-sha"`, `"planDigest": "` + digest + `"`,
	} {
		if !strings.Contains(string(stored), want) {
			t.Errorf("missing %s", want)
		}
	}
}
