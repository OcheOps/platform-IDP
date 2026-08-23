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
		{"Go API Helm values", "helm-values.schema.json", "go-api-helm-values.json"},
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

func TestEverySchemaCompiles(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"common.schema.json",
		"service-request.schema.json",
		"environment.schema.json",
		"helm-values.schema.json",
		"audit-event.schema.json",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := newCompiler(t).Compile(filepath.Join("schemas", name)); err != nil {
				t.Fatalf("compile schema: %v", err)
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

	invalid := validServiceRequest(map[string]any{
		"cpu": "250m", "memory": "256Mi", "minReplicas": 3, "maxReplicas": 2,
	})

	if err := schema.Validate(invalid); err == nil {
		t.Fatal("expected unsafe capacity to be rejected")
	}
}

func TestIndividualCapacityGuardrailsAreRejected(t *testing.T) {
	compiler := newCompiler(t)
	schema, err := compiler.Compile(filepath.Join("schemas", "service-request.schema.json"))
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	tests := []struct {
		name     string
		capacity map[string]any
	}{
		{"unsupported CPU", map[string]any{"cpu": "8", "memory": "256Mi", "minReplicas": 1, "maxReplicas": 2}},
		{"unsupported memory", map[string]any{"cpu": "250m", "memory": "64Gi", "minReplicas": 1, "maxReplicas": 2}},
		{"minimum replicas below bound", map[string]any{"cpu": "250m", "memory": "256Mi", "minReplicas": 0, "maxReplicas": 2}},
		{"maximum replicas above bound", map[string]any{"cpu": "250m", "memory": "256Mi", "minReplicas": 2, "maxReplicas": 20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := validServiceRequest(tt.capacity)
			if err := schema.Validate(request); err == nil {
				t.Fatal("expected invalid capacity to be rejected")
			}
		})
	}
}

func TestInvalidDatabaseAndEnvironmentCombinationsAreRejected(t *testing.T) {
	compiler := newCompiler(t)
	schema, err := compiler.Compile(filepath.Join("schemas", "environment.schema.json"))
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	base := map[string]any{
		"apiVersion": "platform.idp/v1alpha1",
		"kind":       "ServiceEnvironment",
		"metadata": map[string]any{
			"service": "orders-api", "owner": "orders-team", "environment": "staging",
			"requestId": "f4d26f61-ef7d-4bd9-9568-ae621d91187f",
		},
		"spec": map[string]any{
			"template": map[string]any{"id": "go-chi-api", "version": "v0.1.0"},
			"image": map[string]any{
				"repository": "example/orders-api",
				"digest":     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
			"deployment": map[string]any{
				"port":        8080,
				"health":      map[string]any{"livePath": "/health/live", "readyPath": "/health/ready"},
				"resources":   map[string]any{"cpu": "250m", "memory": "256Mi"},
				"autoscaling": map[string]any{"minReplicas": 1, "maxReplicas": 3, "targetCPUUtilization": 70},
			},
			"database": map[string]any{"engine": "postgres"},
			"dns":      map[string]any{"hostname": "orders-api.staging.example.com"},
			"secrets":  []any{},
		},
	}

	t.Run("Postgres requires a secret reference", func(t *testing.T) {
		if err := schema.Validate(base); err == nil {
			t.Fatal("expected postgres without secretRef to be rejected")
		}
	})

	t.Run("Staging rejects preview expiry", func(t *testing.T) {
		spec := base["spec"].(map[string]any)
		spec["database"] = map[string]any{"engine": "postgres", "secretRef": "orders-api-db"}
		spec["expiresAt"] = "2026-08-26T12:00:00Z"
		if err := schema.Validate(base); err == nil {
			t.Fatal("expected staging expiresAt to be rejected")
		}
	})

	t.Run("No database rejects a secret reference", func(t *testing.T) {
		spec := base["spec"].(map[string]any)
		delete(spec, "expiresAt")
		spec["database"] = map[string]any{"engine": "none", "secretRef": "orders-api-db"}
		if err := schema.Validate(base); err == nil {
			t.Fatal("expected database-free service with secretRef to be rejected")
		}
	})
}

func newCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	for _, name := range []string{"common.schema.json", "helm-values.schema.json"} {
		file, err := os.Open(filepath.Join("schemas", name))
		if err != nil {
			t.Fatalf("open shared schema %s: %v", name, err)
		}
		document, err := jsonschema.UnmarshalJSON(file)
		_ = file.Close()
		if err != nil {
			t.Fatalf("decode shared schema %s: %v", name, err)
		}
		url := "https://platform-idp.dev/schemas/" + name
		if err := compiler.AddResource(url, document); err != nil {
			t.Fatalf("register shared schema %s: %v", name, err)
		}
	}
	return compiler
}

func validServiceRequest(capacity map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "platform.idp/v1alpha1",
		"kind":       "ServiceRequest",
		"metadata": map[string]any{
			"name": "orders-api", "owner": "orders-team", "requester": "ocheops",
			"requestId": "f4d26f61-ef7d-4bd9-9568-ae621d91187f",
		},
		"spec": map[string]any{
			"template":    map[string]any{"id": "go-chi-api", "version": "v0.1.0"},
			"environment": "staging",
			"capacity":    capacity,
			"database":    map[string]any{"engine": "postgres"},
		},
	}
}
