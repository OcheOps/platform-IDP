package argocd_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/argocd"
)

func TestObserveRequiresSyncedHealthySuccessfulApplication(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications/orders-api-staging" || r.Header.Get("Authorization") != "Bearer argo-token" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"sync":{"status":"Synced","revision":"git-sha"},"health":{"status":"Healthy"},"operationState":{"phase":"Succeeded"}}}`))
	}))
	server.Listener = listener
	server.StartTLS()
	defer server.Close()
	client, err := argocd.NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "argo-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Observe(context.Background(), "orders-api-staging")
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != argocd.Healthy || observation.Revision != "git-sha" {
		t.Fatalf("unexpected observation: %#v", observation)
	}
}

func TestClientRejectsInsecureURL(t *testing.T) {
	if _, err := argocd.NewClient("http://argo.example.com", http.DefaultClient, func(context.Context) (string, error) { return "token", nil }); err == nil {
		t.Fatal("insecure Argo URL was accepted")
	}
}
