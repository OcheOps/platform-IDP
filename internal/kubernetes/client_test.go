package kubernetes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/kubernetes"
)

func TestClientAppliesWithServerSideApplyAndReads(t *testing.T) {
	var applied map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cluster-token" {
			t.Errorf("missing bearer token")
		}
		switch r.Method + " " + r.URL.Path {
		case "PATCH /apis/batch/v1/namespaces/platform-system/jobs/database-orders":
			if r.Header.Get("Content-Type") != "application/apply-patch+yaml" || r.URL.Query().Get("fieldManager") != kubernetes.FieldManager || r.URL.Query().Has("force") {
				t.Errorf("unexpected apply request %s %v", r.Header.Get("Content-Type"), r.URL.Query())
			}
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &applied)
			w.WriteHeader(http.StatusCreated)
		case "GET /apis/batch/v1/namespaces/platform-system/jobs/database-orders":
			_, _ = w.Write([]byte(`{"metadata":{"name":"database-orders"}}`))
		case "GET /apis/batch/v1/namespaces/platform-system/jobs/missing":
			w.WriteHeader(http.StatusNotFound)
		case "PATCH /apis/batch/v1/namespaces/platform-system/jobs/immutable":
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), func(context.Context) (string, error) { return "cluster-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.Apply(ctx, "/apis/batch/v1/namespaces/platform-system/jobs/database-orders", map[string]string{"kind": "Job"}); err != nil {
		t.Fatal(err)
	}
	if applied["kind"] != "Job" {
		t.Fatalf("applied object = %v", applied)
	}
	var object struct {
		Metadata struct{ Name string } `json:"metadata"`
	}
	if found, err := client.Get(ctx, "/apis/batch/v1/namespaces/platform-system/jobs/database-orders", &object); err != nil || !found || object.Metadata.Name != "database-orders" {
		t.Fatalf("get = %v, %v, %#v", found, err, object)
	}
	if found, err := client.Get(ctx, "/apis/batch/v1/namespaces/platform-system/jobs/missing", &object); err != nil || found {
		t.Fatalf("missing get = %v, %v", found, err)
	}
	if err := client.Apply(ctx, "/apis/batch/v1/namespaces/platform-system/jobs/immutable", map[string]string{}); err == nil {
		t.Fatal("expected immutable-field apply to fail")
	}
}

func TestClientRejectsUnsafeConfiguration(t *testing.T) {
	token := func(context.Context) (string, error) { return "token", nil }
	for _, raw := range []string{"http://cluster.example", "https://user:pass@cluster.example", "https://cluster.example/proxy", "cluster.example"} {
		if _, err := kubernetes.NewClient(raw, http.DefaultClient, token); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	client, err := kubernetes.NewClient("https://cluster.example", http.DefaultClient, token)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/apis/../secrets", "/apis/batch/v1/jobs?watch=1", "jobs"} {
		if _, err := client.Get(context.Background(), path, &struct{}{}); err == nil {
			t.Errorf("accepted path %s", path)
		}
	}
}
