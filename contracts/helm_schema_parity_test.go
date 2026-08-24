package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestHelmChartSchemaMatchesCanonicalContractCorpus(t *testing.T) {
	canonical := compileSchemaAt(t, filepath.Join("schemas", "helm-values.schema.json"), true)
	chart := compileSchemaAt(t, filepath.Join("..", "charts", "service", "values.schema.json"), false)
	valid := readJSONValue(t, filepath.Join("examples", "go-api-helm-values.json"))

	tests := []struct {
		name  string
		valid bool
		edit  func(map[string]any)
	}{
		{name: "valid Go API", valid: true, edit: func(map[string]any) {}},
		{name: "mutable image", edit: func(value map[string]any) { value["image"].(map[string]any)["digest"] = "latest" }},
		{name: "postgres secret missing", edit: func(value map[string]any) { delete(value["database"].(map[string]any), "secretName") }},
		{name: "replica bounds inverted", edit: func(value map[string]any) {
			autoscaling := value["deployment"].(map[string]any)["autoscaling"].(map[string]any)
			autoscaling["minReplicas"], autoscaling["maxReplicas"] = float64(3), float64(2)
		}},
		{name: "remote secret key missing", edit: func(value map[string]any) {
			delete(value["secrets"].([]any)[0].(map[string]any), "remoteKey")
		}},
		{name: "unknown top-level value", edit: func(value map[string]any) { value["privileged"] = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := cloneJSONValue(t, valid)
			tt.edit(value)
			canonicalValid := canonical.Validate(value) == nil
			chartValid := chart.Validate(value) == nil
			if canonicalValid != chartValid {
				t.Fatalf("schema drift: canonical valid=%t, chart valid=%t", canonicalValid, chartValid)
			}
			if canonicalValid != tt.valid {
				t.Fatalf("fixture valid=%t, want %t", canonicalValid, tt.valid)
			}
		})
	}
}

func compileSchemaAt(t *testing.T, path string, canonical bool) *jsonschema.Schema {
	t.Helper()
	if canonical {
		schema, err := newCompiler(t).Compile(path)
		if err != nil {
			t.Fatalf("compile %s: %v", path, err)
		}
		return schema
	}
	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	return schema
}

func readJSONValue(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func cloneJSONValue(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
