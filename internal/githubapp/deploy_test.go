package githubapp_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/deployworker"
	"github.com/OcheOps/platform-IDP/internal/githubapp"
)

func TestObserveImageRequiresSuccessfulPublishedDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/orders-api/commits/main/statuses" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, []any{
			map[string]string{"state": "failure", "context": "platform/image/" + digest, "sha": "old"},
			map[string]string{"state": "success", "context": "platform/image/" + digest, "sha": "main"},
		})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	client, err := githubapp.NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "token", nil })
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.ObserveImage(context.Background(), deployworker.Work{Organization: "acme", ServiceName: "orders-api", AWSAccountID: "123456789012", AWSRegion: "eu-west-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !observation.ImageReady || observation.ImageRepository != "123456789012.dkr.ecr.eu-west-1.amazonaws.com/platform/orders-api" || observation.ImageDigest != digest {
		t.Fatalf("unexpected image observation: %#v", observation)
	}
}
