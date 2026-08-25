package templates_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGoChiTemplateConformance(t *testing.T) {
	directory := filepath.Join("go-chi-api")
	for _, path := range []string{
		"go.mod", "go.sum", "Dockerfile", "Makefile", "README.md", "template.json",
		filepath.Join("cmd", "server", "main.go"), filepath.Join("cmd", "migrate", "main.go"),
		filepath.Join(".github", "workflows", "ci.yaml"), filepath.Join(".platform", "service.json"),
	} {
		if _, err := os.Stat(filepath.Join(directory, path)); err != nil {
			t.Errorf("required template file %s: %v", path, err)
		}
	}

	manifestData, err := os.ReadFile(filepath.Join(directory, "template.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ID             string   `json:"id"`
		Version        string   `json:"version"`
		Language       string   `json:"language"`
		Runtime        string   `json:"runtime"`
		DefaultPort    int      `json:"defaultPort"`
		RequiredInputs []string `json:"requiredInputs"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "go-chi-api" || manifest.Version != "v0.1.0" || manifest.Language != "go" || manifest.Runtime == "" || manifest.DefaultPort != 8080 || len(manifest.RequiredInputs) == 0 {
		t.Fatalf("incomplete template manifest: %#v", manifest)
	}

	workflow, err := os.ReadFile(filepath.Join(directory, ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	actionReference := regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s+(\S+)`)
	immutableAction := regexp.MustCompile(`^[^@\s]+@[a-f0-9]{40}$`)
	actions := actionReference.FindAllSubmatch(workflow, -1)
	if len(actions) == 0 {
		t.Fatal("workflow contains no external actions")
	}
	for _, match := range actions {
		if !immutableAction.Match(match[1]) {
			t.Errorf("external action is not pinned to a commit SHA: %s", match[1])
		}
	}
	jobs := strings.SplitN(string(workflow), "\n  publish:\n", 2)
	if len(jobs) != 2 || strings.Contains(jobs[0], "id-token: write") || !strings.Contains(jobs[1], "id-token: write") {
		t.Fatal("OIDC permission must exist only in the push-only publish job")
	}
	for _, required := range []string{"docker save", "actions/upload-artifact@", "actions/download-artifact@", "docker load"} {
		if !strings.Contains(string(workflow), required) {
			t.Errorf("workflow does not transfer the tested image: missing %q", required)
		}
	}
	for _, required := range []string{
		"postgres:17.6-alpine@sha256:",
		"TEST_DATABASE_URL:",
		"Verify migration command is repeatable",
		"go run ./cmd/migrate",
	} {
		if !strings.Contains(string(workflow), required) {
			t.Errorf("workflow does not exercise PostgreSQL conformance: missing %q", required)
		}
	}

	dockerfile, err := os.ReadFile(filepath.Join(directory, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"golang:1.26.6-alpine3.24@sha256:", "FROM scratch", "USER 65532:65532", "ENTRYPOINT [\"/service\"]"} {
		if !strings.Contains(string(dockerfile), required) {
			t.Errorf("Dockerfile missing %q", required)
		}
	}
	if strings.Contains(string(dockerfile), ":latest") {
		t.Fatal("Dockerfile contains a mutable latest tag")
	}

	command := exec.Command("go", "test", "-race", "./...")
	command.Dir = directory
	command.Env = append(os.Environ(), "GOPROXY=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("template tests failed: %v\n%s", err, output)
	}
}
