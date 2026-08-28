package githubapp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/approvalworker"
	"github.com/OcheOps/platform-IDP/internal/githubapp"
)

func TestApprovalsRequireCurrentHeadDistinctTeamsAndExcludeRequester(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	published := false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/acme/platform-config/pulls/17":
			writeJSON(w, 200, map[string]any{"number": 17, "state": "open", "head": map[string]string{"sha": "head-sha"}})
		case "GET /repos/acme/platform-config/pulls/17/reviews":
			writeJSON(w, 200, []any{review(1, "requester", "APPROVED", "head-sha"), review(2, "alice", "APPROVED", "head-sha"), review(3, "bob", "APPROVED", "head-sha")})
		case "GET /orgs/acme/teams/engineering-owners/memberships/alice", "GET /orgs/acme/teams/platform-owners/memberships/bob":
			writeJSON(w, 200, map[string]string{"state": "active"})
		case "GET /orgs/acme/teams/platform-owners/memberships/alice", "GET /orgs/acme/teams/engineering-owners/memberships/bob":
			w.WriteHeader(404)
		case "POST /repos/acme/platform-config/statuses/head-sha":
			published = true
			writeJSON(w, 201, map[string]any{})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
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
	work := approvalworker.Work{Requester: "requester", Organization: "acme", Repository: "platform-config", HeadSHA: "head-sha", PullRequestNumber: 17, EngineeringOwnerTeam: "engineering-owners", PlatformOwnerTeam: "platform-owners"}
	evidence, ready, err := client.ObserveApprovals(context.Background(), work)
	if err != nil {
		t.Fatal(err)
	}
	if !ready || evidence.EngineeringApprover != "alice" || evidence.PlatformApprover != "bob" {
		t.Fatalf("unexpected evidence: %#v ready=%v", evidence, ready)
	}
	if err := client.PublishApproval(context.Background(), work, evidence); err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("approval status not published")
	}
}
func review(id int64, user, state, commit string) map[string]any {
	return map[string]any{"id": id, "state": state, "commit_id": commit, "user": map[string]string{"login": user}}
}
