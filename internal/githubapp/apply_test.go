package githubapp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/applyworker"
	"github.com/OcheOps/platform-IDP/internal/githubapp"
)

func TestApplyRequiresMergedApprovedHeadAndDigestAttestation(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case "GET /repos/acme/platform-config/pulls/17":
			writeJSON(w, 200, map[string]any{"state": "closed", "merged": true, "merge_commit_sha": "merge-sha", "head": map[string]string{"sha": "head-sha"}})
		case "GET /repos/acme/platform-config/commits/merge-sha/check-runs":
			writeJSON(w, 200, map[string]any{"check_runs": []any{map[string]any{"id": 7, "name": "apply", "status": "completed", "conclusion": "success", "head_sha": "merge-sha", "app": map[string]string{"slug": "github-actions"}}}})
		case "GET /repos/acme/platform-config/commits/merge-sha/statuses":
			writeJSON(w, 200, []any{map[string]string{"state": "success", "context": "platform/apply/" + digest, "sha": "merge-sha"}})
		default:
			t.Errorf("unexpected %s %s", request.Method, request.URL.Path)
			w.WriteHeader(500)
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := githubapp.NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "token", nil })
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.ObserveApply(context.Background(), applyworker.Work{Organization: "acme", Repository: "platform-config", PullRequestNumber: 17, HeadSHA: "head-sha", PlanDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != applyworker.Succeeded || observation.MergeSHA != "merge-sha" {
		t.Fatalf("unexpected observation: %#v", observation)
	}
}
