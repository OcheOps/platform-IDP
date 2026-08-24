package service_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoAPIContractRendersSecureServiceResources(t *testing.T) {
	helm := requireHelm(t)
	values := filepath.Join("..", "..", "contracts", "examples", "go-api-helm-values.json")
	output, err := exec.Command(helm, "template", "orders-api", ".", "--namespace", "svc-orders-api", "--values", values).CombinedOutput()
	if err != nil {
		t.Fatalf("render service chart: %v\n%s", err, output)
	}
	rendered := string(output)
	for _, expected := range []string{
		"kind: Deployment",
		"kind: HorizontalPodAutoscaler",
		"kind: ExternalSecret",
		"kind: Ingress",
		"kind: NetworkPolicy",
		"kind: ServiceMonitor",
		"runAsNonRoot: true",
		"readOnlyRootFilesystem: true",
		"terminationGracePeriodSeconds: 30",
		"secretStoreRef:\n    kind: SecretStore\n    name: service-secrets",
		"port: 5432",
		"orders-api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"name: DATABASE_URL",
		"secretKeyRef:",
		"host: \"orders-api.staging.example.com\"",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered chart missing %q", expected)
		}
	}
}

func TestServiceChartRejectsAmbiguousOrMissingDatabaseSecret(t *testing.T) {
	helm := requireHelm(t)
	values := filepath.Join("..", "..", "contracts", "examples", "go-api-helm-values.json")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "plain text shadows secret",
			args: []string{"--set", "deployment.environment.DATABASE_URL=plaintext"},
			want: "both plain text and a secret",
		},
		{
			name: "database mapping removed",
			args: []string{"--set-json", "secrets=[]"},
			want: "postgres requires a DATABASE_URL secret mapping",
		},
		{
			name: "duplicate secret environment variable",
			args: []string{"--set-json", `secrets=[{"environmentVariable":"DATABASE_URL","secretName":"orders-api-db","key":"url","remoteKey":"platform/orders-api/staging/database"},{"environmentVariable":"DATABASE_URL","secretName":"other-db","key":"url","remoteKey":"platform/orders-api/staging/other"}]`},
			want: "secret environment variable DATABASE_URL must be unique",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"template", "orders-api", ".", "--values", values}
			args = append(args, tt.args...)
			output, err := exec.Command(helm, args...).CombinedOutput()
			if err == nil || !strings.Contains(string(output), tt.want) {
				t.Fatalf("expected %q rejection, got %v\n%s", tt.want, err, output)
			}
		})
	}
}

func TestServiceChartRejectsMutableImageReference(t *testing.T) {
	helm := requireHelm(t)
	output, err := exec.Command(helm, "template", "example", ".", "--set", "image.digest=latest").CombinedOutput()
	if err == nil {
		t.Fatal("expected mutable image reference to be rejected")
	}
	if !strings.Contains(string(output), "/image/digest") {
		t.Fatalf("unexpected validation failure: %s", output)
	}
}

func requireHelm(t *testing.T) string {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("Helm is required for chart conformance tests")
	}
	return helm
}
