package contracts_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestExamplesConformToSchemas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  string
		example string
	}{
		{"Go API service request", "service-request.schema.json", "go-api-service-request.json"},
		{"Go API staging environment", "environment.schema.json", "go-api-staging-environment.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			compiler := newCompiler(t)
			schema, err := compiler.Compile(filepath.Join("schemas", tt.schema))
			if err != nil {
				t.Fatalf("compile schema: %v", err)
			}
			file, err := os.Open(filepath.Join("examples", tt.example))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			t.Cleanup(func() { _ = file.Close() })
			instance, err := jsonschema.UnmarshalJSON(file)
			if err != nil {
				t.Fatalf("decode example: %v", err)
			}
			if err := schema.Validate(instance); err != nil {
				t.Fatalf("validate example: %v", err)
			}
		})
	}
}

func TestInvalidCapacityIsRejected(t *testing.T) {
	compiler := newCompiler(t)
	schema, err := compiler.Compile(filepath.Join("schemas", "service-request.schema.json"))
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	invalid := map[string]any{
		"apiVersion": "platform.idp/v1alpha1",
		"kind":       "ServiceRequest",
		"metadata": map[string]any{
			"name": "orders-api", "owner": "orders-team", "requester": "ocheops",
			"requestId": "f4d26f61-ef7d-4bd9-9568-ae621d91187f",
		},
		"spec": map[string]any{
			"template":    map[string]any{"id": "go-chi-api", "version": "v0.1.0"},
			"environment": "staging",
			"capacity":    map[string]any{"cpu": "8", "memory": "64Gi", "minReplicas": 0, "maxReplicas": 100},
			"database":    map[string]any{"engine": "postgres"},
		},
	}

	if err := schema.Validate(invalid); err == nil {
		t.Fatal("expected unsafe capacity to be rejected")
	}
}

func newCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	file, err := os.Open(filepath.Join("schemas", "helm-values.schema.json"))
	if err != nil {
		t.Fatalf("open shared Helm values schema: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	document, err := jsonschema.UnmarshalJSON(file)
	if err != nil {
		t.Fatalf("decode shared Helm values schema: %v", err)
	}
	if err := compiler.AddResource("https://platform-idp.dev/schemas/helm-values.schema.json", document); err != nil {
		t.Fatalf("register shared Helm values schema: %v", err)
	}
	return compiler
}
