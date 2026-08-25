package goldenpath

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderGoChiProducesBuildableRepositoryFromDeclaredInputs(t *testing.T) {
	target := filepath.Join(t.TempDir(), "orders-api")
	input := GoChiInputs{
		ServiceName:          "orders-api",
		Description:          `Order "capture" API`,
		Owner:                "orders-team",
		GitHubOrganization:   "acme",
		ModulePath:           "github.com/acme/orders-api",
		AWSRegion:            "eu-west-2",
		ECRRepository:        "orders-api",
		GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/orders-api-github",
	}
	if err := RenderGoChi(filepath.Join("..", "..", "templates", "go-chi-api"), target, input); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, expected string }{
		{"go.mod", "module github.com/acme/orders-api"},
		{filepath.Join("cmd", "server", "main.go"), `"github.com/acme/orders-api/internal/app"`},
		{filepath.Join(".platform", "service.json"), `"description": "Order \"capture\" API"`},
		{filepath.Join(".platform", "service.json"), `"githubOrganization": "acme"`},
		{filepath.Join(".platform", "service.json"), `"awsRegion": "eu-west-2"`},
		{filepath.Join(".platform", "service.json"), `"ecrRepository": "orders-api"`},
		{filepath.Join(".github", "CODEOWNERS"), `* @acme/orders-team`},
	} {
		data, err := os.ReadFile(filepath.Join(target, check.path))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), check.expected) {
			t.Errorf("%s missing %q", check.path, check.expected)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(target, ".platform", "service.json"))
	if err != nil || !json.Valid(metadata) {
		t.Fatalf("rendered platform metadata is invalid JSON: %v\n%s", err, metadata)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = target
	command.Env = append(os.Environ(), "GOPROXY=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rendered repository does not test: %v\n%s", err, output)
	}
}

func TestRenderGoChiRejectsInvalidInputAndExistingTarget(t *testing.T) {
	input := GoChiInputs{}
	if err := input.Validate(); err == nil {
		t.Fatal("expected invalid inputs to be rejected")
	}
	target := t.TempDir()
	input = GoChiInputs{
		ServiceName: "orders-api", Description: "Orders", Owner: "orders-team",
		GitHubOrganization: "acme", ModulePath: "github.com/acme/orders-api",
		AWSRegion: "eu-west-1", ECRRepository: "orders-api",
		GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/orders-api-github",
	}
	if err := RenderGoChi(filepath.Join("..", "..", "templates", "go-chi-api"), target, input); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
}
