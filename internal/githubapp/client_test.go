package githubapp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/configworker"
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

func TestClientCreatesIdempotentPlatformConfigPullRequest(t *testing.T) {
	const requestID = "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	branchCreated := false
	pullCreated := false
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/acme/platform-config":
			writeJSON(w, 200, map[string]any{"id": 88, "full_name": "acme/platform-config", "private": true, "description": "Platform IDP desired state"})
		case "GET /repos/acme/platform-config/git/ref/heads/platform%2Frequest-f4d26f61-ef7d-4bd9-9568-ae621d91187f":
			if branchCreated {
				writeJSON(w, 200, map[string]any{"object": map[string]string{"sha": "request-head"}})
			} else {
				w.WriteHeader(404)
			}
		case "GET /repos/acme/platform-config/git/ref/heads/main":
			writeJSON(w, 200, map[string]any{"object": map[string]string{"sha": "main-head"}})
		case "GET /repos/acme/platform-config/git/commits/main-head":
			writeJSON(w, 200, map[string]any{"tree": map[string]string{"sha": "main-tree"}})
		case "POST /repos/acme/platform-config/git/trees":
			writeJSON(w, 201, map[string]string{"sha": "request-tree"})
		case "POST /repos/acme/platform-config/git/commits":
			writeJSON(w, 201, map[string]string{"sha": "request-head"})
		case "POST /repos/acme/platform-config/git/refs":
			branchCreated = true
			writeJSON(w, 201, map[string]any{})
		case "GET /repos/acme/platform-config/contents/services%2Forders-api%2Fstaging%2Frequest.json":
			writeJSON(w, 200, encodedContent(`{"kind":"ServiceRequest","metadata":{"name":"orders-api","requestId":"f4d26f61-ef7d-4bd9-9568-ae621d91187f"}}`+"\n"))
		case "GET /repos/acme/platform-config/contents/services%2Forders-api%2Fstaging%2Festimate.json":
			writeJSON(w, 200, encodedContent(`{"digest":"sha256:abc","incrementalService":{"display":"$10.00"},"previewScenario":{"display":"$1.00"}}`+"\n"))
		case "GET /repos/acme/platform-config/pulls":
			if pullCreated {
				writeJSON(w, 200, []any{configPullResponse()})
			} else {
				writeJSON(w, 200, []any{})
			}
		case "POST /repos/acme/platform-config/pulls":
			pullCreated = true
			writeJSON(w, 201, configPullResponse())
		case "GET /repos/acme/platform-config/pulls/17":
			writeJSON(w, 200, configPullResponse())
		case "GET /repos/acme/platform-config/pulls/17/files":
			writeJSON(w, 200, []map[string]string{{"filename": "services/orders-api/staging/request.json", "status": "added"}, {"filename": "services/orders-api/staging/estimate.json", "status": "added"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
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
	work := configworker.Work{RequestID: requestID, Organization: "acme", ServiceName: "orders-api", Repository: "platform-config", Branch: "platform/request-" + requestID, Request: []byte(`{"kind":"ServiceRequest","metadata":{"name":"orders-api","requestId":"f4d26f61-ef7d-4bd9-9568-ae621d91187f"}}`), Estimate: []byte(`{"digest":"sha256:abc","incrementalService":{"display":"$10.00"},"previewScenario":{"display":"$1.00"}}`)}
	for range 2 {
		sha, err := client.EnsureChange(context.Background(), work)
		if err != nil || sha != "request-head" {
			t.Fatalf("change=%q %v", sha, err)
		}
		number, err := client.EnsurePullRequest(context.Background(), work, sha)
		if err != nil || number != 17 {
			t.Fatalf("pull=%d %v", number, err)
		}
		if err := client.VerifyPullRequest(context.Background(), work, number, sha); err != nil {
			t.Fatal(err)
		}
	}
}

func encodedContent(value string) map[string]string {
	return map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(value))}
}
func configPullResponse() map[string]any {
	return map[string]any{"number": 17, "state": "open", "title": "Provision orders-api in staging", "body": "Platform request `f4d26f61-ef7d-4bd9-9568-ae621d91187f`. Estimate digest: `sha256:abc`", "head": map[string]string{"ref": "platform/request-f4d26f61-ef7d-4bd9-9568-ae621d91187f", "sha": "request-head"}, "base": map[string]string{"ref": "main"}}
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
