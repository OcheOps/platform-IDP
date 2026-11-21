package databaseprovisioner_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestChartRendersHardenedSecretFreeJob(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	args := []string{"template", "orders-db", ".",
		"--namespace", "platform-system", "--set", "image.repository=example.com/platform/database-provisioner", "--set", "image.digest=" + digest,
		"--set", "awsRegion=us-east-1", "--set", "request.id=request-1", "--set", "request.service=orders-api",
		"--set", "database.name=svc_orders", "--set", "database.user=usr_orders", "--set", "database.host=shared.internal",
		"--set", "secrets.master=platform/idp/shared/postgres-master", "--set", "secrets.password=platform/idp/orders/staging/internal/database-password",
		"--set", "secrets.service=platform/idp/orders/staging/database"}
	output, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v: %s", err, output)
	}
	rendered := string(output)
	for _, want := range []string{
		`image: "example.com/platform/database-provisioner@` + digest + `"`, `serviceAccountName: "database-provisioner"`,
		"readOnlyRootFilesystem: true", "allowPrivilegeEscalation: false", "runAsNonRoot: true", "kind: NetworkPolicy",
		"activeDeadlineSeconds: 360", "automountServiceAccountToken: false", "name: AWS_EC2_METADATA_DISABLED", `value: "true"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered chart missing %q", want)
		}
	}
	for _, forbidden := range []string{"master-password", "service-password", "DATABASE_URL:", `cidr: "0.0.0.0/0"`} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("rendered chart contains forbidden secret material %q", forbidden)
		}
	}
}
