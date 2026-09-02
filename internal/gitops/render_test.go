package gitops_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/gitops"
)

func TestRenderStagingValuesUsesImmutableImageAndDatabaseSecretReference(t *testing.T) {
	data, err := gitops.RenderValues(gitops.Work{Service: "orders-api", Owner: "orders-team", Template: "go-chi-api", TemplateVersion: "v0.1.0", Environment: "staging", BaseDomain: "example.com", RequestID: "request-1", ImageRepository: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/platform/orders-api", ImageDigest: "sha256:" + strings.Repeat("a", 64), CPU: "250m", Memory: "256Mi", MinReplicas: 1, MaxReplicas: 3, TargetCPU: 70, Database: true, DatabaseSecretName: "platform/idp/orders-api/staging/database"})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	image := values["image"].(map[string]any)
	if image["digest"] != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("image is not digest pinned: %#v", image)
	}
	database := values["database"].(map[string]any)
	if database["engine"] != "postgres" || database["migrationCommand"] != "/migrate" {
		t.Fatalf("unexpected database values: %#v", database)
	}
	secrets := values["secrets"].([]any)
	if len(secrets) != 1 || secrets[0].(map[string]any)["remoteKey"] != "platform/idp/orders-api/staging/database" {
		t.Fatalf("unexpected secret mapping: %#v", secrets)
	}
}

func TestApplicationSetRestrictsDiscoveryToStagingValues(t *testing.T) {
	data, err := gitops.RenderApplicationSet("https://github.com/acme/platform-config.git")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(data)
	for _, want := range []string{"directories:", "services/*/staging", "revision: main", "'{{path}}/values.json'", "CreateNamespace=true"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("ApplicationSet missing %q", want)
		}
	}
}
