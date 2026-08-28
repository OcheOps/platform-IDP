package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureCollectsRoute53ConfigAndUsesDefaults(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		"acme-platform",
		"123456789012",
		"eu-west-1",
		"platform-admin",
		"acme",
		"",
		"",
		"route53",
		"Z123",
		"acme.example",
		"platform@acme.example",
		"1.35",
		"198.51.100.10/32",
		"",
		"",
		"",
	}, "\n") + "\n")
	var output bytes.Buffer

	config, err := Configure(input, &output)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("configured value is invalid: %v", err)
	}
	if config.Cluster.NodeInstanceTypes[0] != "m7i.large" || config.Cluster.MinimumNodes != 2 || config.Cluster.MaximumNodes != 4 {
		t.Fatalf("defaults not applied: %#v", config.Cluster)
	}
	if !strings.Contains(output.String(), "No credentials or secret values") {
		t.Fatalf("missing secret-safety guidance: %s", output.String())
	}
}

func TestConfigureCloudflareCollectsOnlySecretReference(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		"acme-platform", "123456789012", "eu-west-1", "", "acme", "", "",
		"cloudflare", "zone-id", "acme.example", "platform@acme.example",
		"cloudflare-api-token", "1.35", "198.51.100.10/32", "", "", "",
	}, "\n") + "\n")

	config, err := Configure(input, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.DNS.CloudflareTokenSecretName != "cloudflare-api-token" {
		t.Fatalf("unexpected secret reference: %q", config.DNS.CloudflareTokenSecretName)
	}
	serialized, err := EncodeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(serialized)), "token-value") {
		t.Fatal("serialized config contains a secret value")
	}
}

func TestWriteConfigIsPrivateAndRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := WriteConfig(path, validConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	if err := WriteConfig(path, validConfig()); err == nil {
		t.Fatal("expected existing config to be preserved")
	}
}
