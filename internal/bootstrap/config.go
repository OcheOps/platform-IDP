package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	InstallationID string        `json:"installationId"`
	AWS            AWSConfig     `json:"aws"`
	GitHub         GitHubConfig  `json:"github"`
	DNS            DNSConfig     `json:"dns"`
	Network        NetworkConfig `json:"network"`
	Cluster        ClusterConfig `json:"cluster"`
}

type AWSConfig struct {
	AccountID string `json:"accountId"`
	Region    string `json:"region"`
	Profile   string `json:"profile,omitempty"`
}

type GitHubConfig struct {
	Organization         string `json:"organization"`
	EngineeringOwnerTeam string `json:"engineeringOwnerTeam"`
	PlatformOwnerTeam    string `json:"platformOwnerTeam"`
}

type DNSConfig struct {
	Provider                  string `json:"provider"`
	ZoneID                    string `json:"zoneId"`
	Domain                    string `json:"domain"`
	CloudflareTokenSecretName string `json:"cloudflareTokenSecretName,omitempty"`
	ACMEEmail                 string `json:"acmeEmail"`
}

type NetworkConfig struct {
	VPCCIDR string `json:"vpcCidr"`
}

type ClusterConfig struct {
	KubernetesVersion string   `json:"kubernetesVersion"`
	NodeInstanceTypes []string `json:"nodeInstanceTypes"`
	PublicAccessCIDRs []string `json:"publicAccessCidrs"`
	MinimumNodes      int      `json:"minimumNodes"`
	MaximumNodes      int      `json:"maximumNodes"`
}

var (
	idPattern                     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	regionPattern                 = regexp.MustCompile(`^[a-z]{2}(?:-gov)?-[a-z]+-[0-9]$`)
	domainPattern                 = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)
	kubernetesDNSSubdomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
)

func DefaultConfig() Config {
	return Config{
		GitHub:  GitHubConfig{EngineeringOwnerTeam: "engineering-owners", PlatformOwnerTeam: "platform-owners"},
		Network: NetworkConfig{VPCCIDR: "10.42.0.0/16"},
		Cluster: ClusterConfig{
			NodeInstanceTypes: []string{"m7i.large"},
			MinimumNodes:      2,
			MaximumNodes:      4,
		},
	}
}

func (c Config) Validate() error {
	var problems []string
	if !idPattern.MatchString(c.InstallationID) {
		problems = append(problems, "installationId must be a 3-32 character lowercase DNS label")
	}
	if !regionPattern.MatchString(c.AWS.Region) {
		problems = append(problems, "aws.region is not a valid AWS region name")
	}
	if matched, _ := regexp.MatchString(`^[0-9]{12}$`, c.AWS.AccountID); !matched {
		problems = append(problems, "aws.accountId must be a 12-digit account ID")
	}
	if c.GitHub.Organization == "" {
		problems = append(problems, "github.organization is required")
	}
	if !idPattern.MatchString(c.GitHub.EngineeringOwnerTeam) || !idPattern.MatchString(c.GitHub.PlatformOwnerTeam) {
		problems = append(problems, "github owner teams must be 3-32 character lowercase team slugs")
	} else if c.GitHub.EngineeringOwnerTeam == c.GitHub.PlatformOwnerTeam {
		problems = append(problems, "github engineering and platform owner teams must be distinct")
	}
	if c.DNS.Provider != "route53" && c.DNS.Provider != "cloudflare" {
		problems = append(problems, "dns.provider must be route53 or cloudflare")
	}
	if c.DNS.ZoneID == "" {
		problems = append(problems, "dns.zoneId is required")
	}
	if !domainPattern.MatchString(c.DNS.Domain) {
		problems = append(problems, "dns.domain must be a valid domain")
	}
	if c.DNS.Provider == "cloudflare" && c.DNS.CloudflareTokenSecretName == "" {
		problems = append(problems, "dns.cloudflareTokenSecretName is required for Cloudflare")
	} else if c.DNS.CloudflareTokenSecretName != "" && !validKubernetesDNSSubdomain(c.DNS.CloudflareTokenSecretName) {
		problems = append(problems, "dns.cloudflareTokenSecretName must be a valid Kubernetes DNS subdomain")
	}
	if address, err := mail.ParseAddress(c.DNS.ACMEEmail); err != nil || address.Address != c.DNS.ACMEEmail {
		problems = append(problems, "dns.acmeEmail must be a valid email address")
	}
	if c.Network.VPCCIDR != "10.42.0.0/16" {
		problems = append(problems, "network.vpcCidr must be 10.42.0.0/16 in the first Phase 1 slice")
	}
	if c.Cluster.KubernetesVersion == "" {
		problems = append(problems, "cluster.kubernetesVersion is required")
	}
	if len(c.Cluster.NodeInstanceTypes) == 0 {
		problems = append(problems, "cluster.nodeInstanceTypes requires at least one instance type")
	}
	if len(c.Cluster.PublicAccessCIDRs) == 0 {
		problems = append(problems, "cluster.publicAccessCidrs requires at least one operator CIDR")
	}
	for _, cidr := range c.Cluster.PublicAccessCIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			problems = append(problems, fmt.Sprintf("cluster.publicAccessCidrs contains invalid CIDR %q", cidr))
		} else if prefix.Bits() == 0 {
			problems = append(problems, "cluster.publicAccessCidrs cannot expose the EKS API globally")
		}
	}
	if c.Cluster.MinimumNodes < 2 || c.Cluster.MaximumNodes < c.Cluster.MinimumNodes || c.Cluster.MaximumNodes > 10 {
		problems = append(problems, "cluster nodes must satisfy 2 <= minimumNodes <= maximumNodes <= 10")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validKubernetesDNSSubdomain(value string) bool {
	if len(value) > 253 || !kubernetesDNSSubdomainPattern.MatchString(value) {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func ReadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("decode config: trailing JSON value")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func WriteGeneratedFiles(outputDir string, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	files := []struct{ name, contents string }{
		{"backend.hcl", backendConfig(config)},
		{"addons-backend.hcl", addonsBackendConfig(config)},
		{"bootstrap-state.auto.tfvars.json", stateVariables(config)},
		{"foundation.auto.tfvars.json", terraformVariables(config)},
		{"addons.auto.tfvars.json", addonsVariables(config)},
	}
	for _, generated := range files {
		path := filepath.Join(outputDir, generated.name)
		if err := writeGenerated(path, strings.NewReader(generated.contents)); err != nil {
			return err
		}
	}
	return nil
}

func addonsBackendConfig(config Config) string {
	return remoteBackendConfig(config, "addons/terraform.tfstate")
}

func addonsVariables(config Config) string {
	value := map[string]any{
		"installation_id":              config.InstallationID,
		"aws_region":                   config.AWS.Region,
		"aws_profile":                  config.AWS.Profile,
		"state_bucket_name":            stateBucket(config),
		"state_lock_table_name":        stateLockTable(config),
		"dns_provider":                 config.DNS.Provider,
		"dns_zone_id":                  config.DNS.ZoneID,
		"base_domain":                  config.DNS.Domain,
		"acme_email":                   config.DNS.ACMEEmail,
		"cloudflare_token_secret_name": config.DNS.CloudflareTokenSecretName,
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data) + "\n"
}

func backendConfig(config Config) string {
	return remoteBackendConfig(config, "foundation/terraform.tfstate")
}

func remoteBackendConfig(config Config, key string) string {
	value := fmt.Sprintf("bucket = %q\nkey = %q\nregion = %q\ndynamodb_table = %q\nencrypt = true\n", stateBucket(config), key, config.AWS.Region, stateLockTable(config))
	if config.AWS.Profile != "" {
		value += fmt.Sprintf("profile = %q\n", config.AWS.Profile)
	}
	return value
}

func stateVariables(config Config) string {
	value := map[string]any{
		"installation_id":   config.InstallationID,
		"aws_region":        config.AWS.Region,
		"aws_profile":       config.AWS.Profile,
		"state_bucket_name": stateBucket(config),
		"lock_table_name":   stateLockTable(config),
		"tags": map[string]string{
			"platform-idp:managed":      "true",
			"platform-idp:installation": config.InstallationID,
			"platform-idp:service":      "shared",
			"platform-idp:environment":  "platform",
			"platform-idp:cost-scope":   "shared",
		},
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data) + "\n"
}

func terraformVariables(config Config) string {
	value := map[string]any{
		"installation_id":             config.InstallationID,
		"aws_region":                  config.AWS.Region,
		"aws_profile":                 config.AWS.Profile,
		"github_organization":         config.GitHub.Organization,
		"dns_provider":                config.DNS.Provider,
		"dns_zone_id":                 config.DNS.ZoneID,
		"base_domain":                 config.DNS.Domain,
		"vpc_cidr":                    config.Network.VPCCIDR,
		"kubernetes_version":          config.Cluster.KubernetesVersion,
		"node_instance_types":         config.Cluster.NodeInstanceTypes,
		"cluster_public_access_cidrs": config.Cluster.PublicAccessCIDRs,
		"node_minimum_size":           config.Cluster.MinimumNodes,
		"node_maximum_size":           config.Cluster.MaximumNodes,
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data) + "\n"
}

func stateBucket(config Config) string {
	return fmt.Sprintf("%s-%s-%s-tofu-state", config.InstallationID, config.AWS.AccountID, config.AWS.Region)
}

func stateLockTable(config Config) string { return config.InstallationID + "-tofu-locks" }

func writeGenerated(path string, source io.Reader) error {
	data, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == string(data) {
			return nil
		}
		return fmt.Errorf("refusing to overwrite modified generated file %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".idp-generated-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
