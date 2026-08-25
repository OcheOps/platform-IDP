package githubapp_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/githubapp"
	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

func TestClientCreatesInitialRepositoryAndVerifiesPolicy(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("# Orders\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".github", "ci.yaml"), []byte("name: ci\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	rulesetCreated := false
	refCreated := false
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer installation-token" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Errorf("missing GitHub headers")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/acme/orders-api":
			w.WriteHeader(404)
		case "POST /orgs/acme/repos":
			writeJSON(w, 201, repositoryResponse())
		case "GET /repos/acme/orders-api/git/ref/heads/main":
			if refCreated {
				writeJSON(w, 200, map[string]any{"object": map[string]string{"sha": "commit-sha"}})
			} else {
				w.WriteHeader(404)
			}
		case "POST /repos/acme/orders-api/git/trees":
			writeJSON(w, 201, map[string]string{"sha": "bc1c053c4af07e3559d2b4ea37b2d3de1a71f698"})
		case "POST /repos/acme/orders-api/git/commits":
			writeJSON(w, 201, map[string]string{"sha": "commit-sha"})
		case "POST /repos/acme/orders-api/git/refs":
			refCreated = true
			writeJSON(w, 201, map[string]any{})
		case "PATCH /repos/acme/orders-api":
			writeJSON(w, 200, map[string]any{})
		case "PUT /orgs/acme/teams/orders-team/repos/acme/orders-api":
			w.WriteHeader(204)
		case "GET /repos/acme/orders-api/rulesets":
			if rulesetCreated {
				writeJSON(w, 200, []map[string]any{{"id": 7, "name": "platform-main-protection", "enforcement": "active"}})
			} else {
				writeJSON(w, 200, []any{})
			}
		case "POST /repos/acme/orders-api/rulesets":
			rulesetCreated = true
			writeJSON(w, 201, map[string]any{"id": 7})
		case "GET /repositories/42":
			writeJSON(w, 200, repositoryResponse())
		case "GET /repos/acme/orders-api/git/commits/commit-sha":
			writeJSON(w, 200, map[string]any{"message": "Initialize service from platform golden path", "parents": []any{}, "tree": map[string]string{"sha": "bc1c053c4af07e3559d2b4ea37b2d3de1a71f698"}})
		case "GET /repos/acme/orders-api/rulesets/7":
			writeJSON(w, 200, rulesetResponse())
		case "GET /orgs/acme/teams/orders-team/repos/acme/orders-api":
			writeJSON(w, 200, map[string]any{"permissions": map[string]bool{"maintain": true}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := githubapp.NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "installation-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	work := repositoryworker.Work{RequestID: "request-1", Organization: "acme", Name: "orders-api", OwnerTeam: "orders-team", RenderedPath: directory}
	id, err := client.EnsureRepository(context.Background(), work)
	if err != nil || id != 42 {
		t.Fatalf("repository = %d, %v", id, err)
	}
	if err := client.EnsureInitialCommit(context.Background(), work, id); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureSettings(context.Background(), work, id); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background(), work, id); err != nil {
		t.Fatal(err)
	}
	work.RepositoryID = id
	if recoveredID, err := client.EnsureRepository(context.Background(), work); err != nil || recoveredID != id {
		t.Fatalf("recovery repository = %d, %v", recoveredID, err)
	}
	if err := client.EnsureInitialCommit(context.Background(), work, id); err != nil {
		t.Fatalf("recover initial commit: %v", err)
	}
	if err := client.EnsureSettings(context.Background(), work, id); err != nil {
		t.Fatalf("recover settings: %v", err)
	}
	if !containsCall(calls, "POST /repos/acme/orders-api/git/trees") || !containsCall(calls, "POST /repos/acme/orders-api/rulesets") {
		t.Fatalf("missing provider calls: %#v", calls)
	}
	if countCall(calls, "POST /orgs/acme/repos") != 1 || countCall(calls, "POST /repos/acme/orders-api/git/trees") != 1 || countCall(calls, "POST /repos/acme/orders-api/rulesets") != 1 {
		t.Fatalf("recovery repeated creation: %#v", calls)
	}
}

func TestClientRejectsInsecureRemoteAPI(t *testing.T) {
	if _, err := githubapp.NewClient("http://github.example.com", http.DefaultClient, func(context.Context) (string, error) { return "token", nil }); err == nil {
		t.Fatal("insecure remote GitHub API URL was accepted")
	}
}

func repositoryResponse() map[string]any {
	return map[string]any{"id": 42, "full_name": "acme/orders-api", "private": true, "description": "Managed by Platform IDP request request-1", "default_branch": "main", "delete_branch_on_merge": true, "allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": false}
}
func rulesetResponse() map[string]any {
	return map[string]any{
		"id": 7, "name": "platform-main-protection", "target": "branch", "enforcement": "active", "bypass_actors": []any{},
		"conditions": map[string]any{"ref_name": map[string]any{"include": []string{"refs/heads/main"}, "exclude": []string{}}},
		"rules": []map[string]any{
			{"type": "deletion"}, {"type": "non_fast_forward"}, {"type": "required_linear_history"},
			{"type": "required_status_checks", "parameters": map[string]any{"strict_required_status_checks_policy": true, "required_status_checks": []map[string]string{{"context": "build"}}}},
			{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": 1, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true, "require_last_push_approval": true, "required_review_thread_resolution": true}},
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}
func countCall(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}
