package githubapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/configworker"
)

func TestPlatformRepositoryTemplateContainsPinnedPlanAndRealResources(t *testing.T) {
	work := configworker.Work{Platform: configworker.Platform{InstallationID: "small-team-idp", AWSAccountID: "123456789012", AWSRegion: "eu-west-1", AWSRoleARN: "arn:aws:iam::123456789012:role/platform-config-plan", StateBucket: "small-team-idp-123456789012-eu-west-1-tofu-state", LockTable: "small-team-idp-tofu-locks", BaseDomain: "example.com"}}
	files, err := platformRepositoryFiles(work)
	if err != nil {
		t.Fatal(err)
	}
	workflow := files[".github/workflows/plan.yaml"]
	for _, want := range []string{"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd", "opentofu/setup-opentofu@a1320f892987e89d278cc92dc5adc984fb93aca4", "aws-actions/configure-aws-credentials@00943011d9042930efac3dcd3a170e4273319bc8", "tofu -chdir=infrastructure plan", "rm -f \"$RUNNER_TEMP/service.tfplan\""} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
	applyWorkflow := files[".github/workflows/apply.yaml"]
	for _, want := range []string{"concurrency:", "platform-config-apply", "actual_digest", `test "$actual_digest" = "$approved_digest"`, `state:"pending"`, "receipt_exists", `change.actions == ["no-op"]`, "tofu -chdir=infrastructure apply", "platform/apply/$approved_digest"} {
		if !strings.Contains(applyWorkflow, want) {
			t.Errorf("apply workflow missing %q", want)
		}
	}
	for _, want := range []string{`resource "aws_ecr_repository"`, `resource "aws_secretsmanager_secret"`, `data "terraform_remote_state" "foundation"`, `resource "aws_iam_role" "service_secrets"`, `resource "aws_iam_role_policy" "service_secrets"`, `system:serviceaccount:svc-${each.key}:service-secrets`, `local.resource_identities["${var.installation_id}/${each.key}/staging"].secretStoreRoleName`} {
		if !strings.Contains(files["infrastructure/main.tf"], want) {
			t.Errorf("module missing %q", want)
		}
	}
	if output, err := exec.Command("python3", "-c", "compile("+strconv.Quote(files["scripts/validate.py"])+", '<validate.py>', 'exec')").CombinedOutput(); err != nil {
		t.Fatalf("invalid validator: %v: %s", err, output)
	}
	terraform, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform is unavailable for generated HCL format validation")
	}
	directory := t.TempDir()
	for _, name := range []string{"main.tf", "variables.tf", "versions.tf"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(files["infrastructure/"+name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := exec.Command(terraform, "fmt", "-check", "-diff", directory).CombinedOutput(); err != nil {
		t.Fatalf("generated HCL is not formatted: %v: %s", err, output)
	}
}

func TestConfigFilesIncludeReviewedResourceIdentities(t *testing.T) {
	requestID := "f4d26f61-ef7d-4bd9-9568-ae621d91187f"
	work := configworker.Work{
		RequestID: requestID, ServiceName: "orders-api",
		Request:  []byte(`{"metadata":{"name":"orders-api","requestId":"` + requestID + `"},"spec":{"database":{"engine":"postgres"}}}`),
		Estimate: []byte(`{"digest":"sha256:abc"}`),
		Platform: configworker.Platform{InstallationID: "small-team-idp", AWSAccountID: "123456789012"},
	}
	files, err := configFiles(work)
	if err != nil {
		t.Fatal(err)
	}
	resources := string(files["services/orders-api/staging/resources.json"])
	for _, want := range []string{`"canonicalId": "small-team-idp/orders-api/staging"`, `"databaseSecretName": "platform/small-team-idp/orders-api/staging/database"`, `"databasePasswordSecretName": "platform/small-team-idp/orders-api/staging/internal/database-password"`, `"databaseName": "svc_orders_api_8c7a25e2"`, `"databaseUser": "usr_orders_api_8c7a25e2"`, `"secretStoreRoleName": "small-team-idp-orders-api-staging-secrets-8c7a25e2"`} {
		if !strings.Contains(resources, want) {
			t.Errorf("resources file missing %s: %s", want, resources)
		}
	}
}
