package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureCommandWritesConfigAndRefusesExistingTargetBeforePrompting(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	answers := strings.Join([]string{
		"acme-platform", "123456789012", "eu-west-1", "platform-admin", "acme",
		"", "",
		"route53", "Z123", "acme.example", "platform@acme.example", "1.35",
		"198.51.100.10/32", "", "", "",
	}, "\n") + "\n"
	var output bytes.Buffer

	if err := runWithIO(context.Background(), []string{"configure", "--config", path}, strings.NewReader(answers), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	if err := runWithIO(context.Background(), []string{"configure", "--config", path}, strings.NewReader(""), &output); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("expected early overwrite refusal, got %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("wizard started before overwrite check: %q", output.String())
	}
}

func TestExistingValidateAndRenderCommandsRemainReachable(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "bootstrap", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runWithIO(context.Background(), []string{"validate", "--config", configPath}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "configuration is valid") {
		t.Fatalf("unexpected validate output: %q", output.String())
	}

	generated := filepath.Join(directory, "generated")
	if err := runWithIO(context.Background(), []string{"render", "--config", configPath, "--output", generated}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(generated, "foundation.auto.tfvars.json")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRequiresExplicitStage(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.json")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "bootstrap", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	err = runWithIO(context.Background(), []string{"install", "--config", configPath}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--stage is required") {
		t.Fatalf("expected stage error, got %v", err)
	}
}
