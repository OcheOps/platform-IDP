package bootstrap

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type installRunner struct {
	calls      [][]string
	failAt     int
	createPlan string
}

func (r *installRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.failAt == len(r.calls) {
		return []byte("secret-looking provider output"), errors.New("exit status 1")
	}
	if r.createPlan != "" && len(r.calls) == 2 {
		if err := os.WriteFile(r.createPlan, []byte("saved plan"), 0o644); err != nil {
			return nil, err
		}
	}
	return []byte("ok"), nil
}

func TestPlanAndApplyRequireReviewedDigestAndRemovePlan(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	stage, err := InstallationStage("foundation", "/repo", directory)
	if err != nil {
		t.Fatal(err)
	}
	runner := &installRunner{createPlan: stage.PlanFile}
	digest, err := PlanInstallationStage(context.Background(), runner, stage, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(stage.PlanFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("plan mode = %o, want 600", info.Mode().Perm())
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("plan directory mode = %o, want 700", directoryInfo.Mode().Perm())
	}
	if err := ApplyInstallationStage(context.Background(), runner, stage, "wrong", &strings.Builder{}); err == nil {
		t.Fatal("expected mismatched digest to be rejected")
	}
	if err := ApplyInstallationStage(context.Background(), runner, stage, digest, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage.PlanFile); !os.IsNotExist(err) {
		t.Fatalf("plan remains after apply: %v", err)
	}
}

func TestPlanFailureDoesNotRedisplayProviderOutput(t *testing.T) {
	stage, err := InstallationStage("foundation", "/repo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanInstallationStage(context.Background(), &installRunner{failAt: 1}, stage, &strings.Builder{})
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "secret-looking") {
		t.Fatalf("provider output was redisplayed: %v", err)
	}
}

func TestCloudflarePreparationHasActionableSecretFailure(t *testing.T) {
	config := validConfig()
	config.DNS.Provider = "cloudflare"
	config.DNS.CloudflareTokenSecretName = "cloudflare-api-token"
	err := PrepareAddons(context.Background(), &installRunner{failAt: 2}, config)
	if err == nil || !strings.Contains(err.Error(), "docs/phase-1/bootstrap.md") {
		t.Fatalf("expected documented recovery, got %v", err)
	}
}

func TestUnknownInstallationStageIsRejected(t *testing.T) {
	if _, err := InstallationStage("unknown", "/repo", "/generated"); err == nil {
		t.Fatal("expected unknown stage failure")
	}
}

func TestPostflightChecksHTTPSAndDoesNotRedisplayFailures(t *testing.T) {
	runner := &installRunner{failAt: 10}
	err := RunPostflight(context.Background(), runner, validConfig(), &strings.Builder{})
	if err == nil {
		t.Fatal("expected HTTPS failure")
	}
	if strings.Contains(err.Error(), "secret-looking") {
		t.Fatalf("postflight redisplayed command output: %v", err)
	}
	last := runner.calls[len(runner.calls)-1]
	if last[0] != "curl" || last[len(last)-1] != "https://argocd.acme.example" {
		t.Fatalf("unexpected HTTPS check: %#v", last)
	}
}
