package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validConfig() Config {
	config := DefaultConfig()
	config.InstallationID = "acme-platform"
	config.AWS = AWSConfig{AccountID: "123456789012", Region: "eu-west-1", Profile: "platform-admin"}
	config.GitHub = GitHubConfig{Organization: "acme"}
	config.DNS = DNSConfig{Provider: "route53", ZoneID: "Z123", Domain: "acme.example", ACMEEmail: "platform@acme.example"}
	config.Cluster.KubernetesVersion = "1.35"
	config.Cluster.PublicAccessCIDRs = []string{"198.51.100.10/32"}
	return config
}

func TestConfigValidation(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	config := validConfig()
	config.Cluster.MinimumNodes = 5
	config.Cluster.MaximumNodes = 2
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "minimumNodes") {
		t.Fatalf("expected node bounds error, got %v", err)
	}
}

func TestConfigRejectsGlobalOrInvalidClusterAccessCIDRs(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "::/0", "not-a-cidr"} {
		config := validConfig()
		config.Cluster.PublicAccessCIDRs = []string{cidr}
		if err := config.Validate(); err == nil {
			t.Fatalf("expected %q to be rejected", cidr)
		}
	}
}

func TestCloudflareRequiresSecretReference(t *testing.T) {
	config := validConfig()
	config.DNS.Provider = "cloudflare"
	config.DNS.CloudflareTokenSecretName = ""
	if err := config.Validate(); err == nil {
		t.Fatal("expected Cloudflare without a token Secret reference to be rejected")
	}
}

func TestCloudflareSecretReferenceMustBeAKubernetesName(t *testing.T) {
	for _, name := range []string{"Cloudflare Token", "-cloudflare", "cloudflare-", strings.Repeat("a", 254)} {
		config := validConfig()
		config.DNS.Provider = "cloudflare"
		config.DNS.CloudflareTokenSecretName = name
		if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "Kubernetes DNS subdomain") {
			t.Fatalf("expected %q to be rejected, got %v", name, err)
		}
	}
}

func TestGeneratedAddonConfigurationUsesSeparateStateAndReferencesOnly(t *testing.T) {
	directory := t.TempDir()
	config := validConfig()
	config.DNS.Provider = "cloudflare"
	config.DNS.CloudflareTokenSecretName = "cloudflare-api-token"
	if err := WriteGeneratedFiles(directory, config); err != nil {
		t.Fatal(err)
	}
	backend, err := os.ReadFile(filepath.Join(directory, "addons-backend.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backend), `key = "addons/terraform.tfstate"`) {
		t.Fatalf("unexpected add-ons backend: %s", backend)
	}
	variables, err := os.ReadFile(filepath.Join(directory, "addons.auto.tfvars.json"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(variables)
	for _, expected := range []string{`"dns_provider": "cloudflare"`, `"cloudflare_token_secret_name": "cloudflare-api-token"`} {
		if !strings.Contains(contents, expected) {
			t.Fatalf("generated add-ons variables missing %s: %s", expected, contents)
		}
	}
}

func TestGeneratedFilesContainNoCredentialsAndAreNotOverwritten(t *testing.T) {
	directory := t.TempDir()
	config := validConfig()
	if err := WriteGeneratedFiles(directory, config); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backend.hcl", "addons-backend.hcl", "bootstrap-state.auto.tfvars.json", "foundation.auto.tfvars.json", "addons.auto.tfvars.json"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(data)
		for _, forbidden := range []string{"access_key", "secret_key", "password", "plaintext-secret-value"} {
			if strings.Contains(strings.ToLower(contents), forbidden) {
				t.Fatalf("%s contains forbidden credential field %q", name, forbidden)
			}
		}
	}
	if err := WriteGeneratedFiles(directory, config); err != nil {
		t.Fatalf("identical rerender should succeed: %v", err)
	}
}

type fakeRunner struct {
	calls [][]string
	fail  string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name == r.fail {
		return []byte("missing"), errors.New("not found")
	}
	if name == "aws" && len(args) > 0 && args[0] == "sts" {
		return []byte(`{"Account":"123456789012"}`), nil
	}
	return []byte("ok"), nil
}

func TestPreflightUsesConfiguredAWSProfileAndReportsFailures(t *testing.T) {
	runner := &fakeRunner{fail: "tofu"}
	results := RunPreflight(context.Background(), runner, validConfig())
	if PreflightPassed(results) {
		t.Fatal("expected preflight failure")
	}
	want := []string{"aws", "sts", "get-caller-identity", "--output", "json", "--region", "eu-west-1", "--profile", "platform-admin"}
	found := false
	for _, call := range runner.calls {
		if reflect.DeepEqual(call, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("AWS identity call missing; calls: %#v", runner.calls)
	}
}
