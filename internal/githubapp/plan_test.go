package githubapp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/githubapp"
	"github.com/OcheOps/platform-IDP/internal/planworker"
)

func TestObservePlanBindsSuccessfulCheckAndDigestToHead(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/platform-config/pulls/17":
			writeJSON(w, 200, map[string]any{"number": 17, "state": "open", "head": map[string]string{"sha": "head-sha"}})
		case "/repos/acme/platform-config/commits/head-sha/check-runs":
			writeJSON(w, 200, map[string]any{"check_runs": []any{map[string]any{"id": 2, "name": "build", "status": "completed", "conclusion": "success", "head_sha": "head-sha", "app": map[string]string{"slug": "github-actions"}}}})
		case "/repos/acme/platform-config/commits/head-sha/statuses":
			writeJSON(w, 200, []any{map[string]string{"state": "success", "context": "platform/plan/" + digest, "sha": "head-sha"}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
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
	observed, err := client.ObservePlan(context.Background(), planworker.Work{Organization: "acme", Repository: "platform-config", PullRequestNumber: 17, HeadSHA: "head-sha", EstimateDigest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != planworker.Succeeded || observed.PlanDigest != digest {
		t.Fatalf("unexpected observation: %#v", observed)
	}
}
