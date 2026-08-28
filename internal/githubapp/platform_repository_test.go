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
	for _, want := range []string{`resource "aws_ecr_repository"`, `resource "aws_secretsmanager_secret"`} {
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
