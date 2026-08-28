package bootstrap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Configure collects only durable, non-secret bootstrap settings.
func Configure(input io.Reader, output io.Writer) (Config, error) {
	scanner := bufio.NewScanner(input)
	config := DefaultConfig()

	fmt.Fprintln(output, "Platform IDP guided configuration")
	fmt.Fprintln(output, "No credentials or secret values are requested or written.")
	fmt.Fprintln(output)

	config.InstallationID = prompt(scanner, output, "Installation ID", "")
	config.AWS.AccountID = prompt(scanner, output, "AWS account ID", "")
	config.AWS.Region = prompt(scanner, output, "AWS region", "")
	config.AWS.Profile = prompt(scanner, output, "AWS CLI profile (blank uses the active environment)", "")
	config.GitHub.Organization = prompt(scanner, output, "GitHub organization", "")
	config.GitHub.EngineeringOwnerTeam = prompt(scanner, output, "Engineering owner team", config.GitHub.EngineeringOwnerTeam)
	config.GitHub.PlatformOwnerTeam = prompt(scanner, output, "Platform owner team", config.GitHub.PlatformOwnerTeam)
	config.DNS.Provider = strings.ToLower(prompt(scanner, output, "DNS provider (route53/cloudflare)", "route53"))
	config.DNS.ZoneID = prompt(scanner, output, "DNS zone ID", "")
	config.DNS.Domain = strings.ToLower(prompt(scanner, output, "Base domain", ""))
	config.DNS.ACMEEmail = prompt(scanner, output, "ACME email", "")
	if config.DNS.Provider == "cloudflare" {
		config.DNS.CloudflareTokenSecretName = prompt(scanner, output, "Kubernetes Secret name containing the Cloudflare token", "cloudflare-api-token")
	}
	config.Cluster.KubernetesVersion = prompt(scanner, output, "Kubernetes version", "")
	config.Cluster.PublicAccessCIDRs = splitCSV(prompt(scanner, output, "Operator CIDRs (comma-separated)", ""))
	config.Cluster.NodeInstanceTypes = splitCSV(prompt(scanner, output, "Node instance types (comma-separated)", strings.Join(config.Cluster.NodeInstanceTypes, ",")))

	minimum, err := promptInt(scanner, output, "Minimum nodes", config.Cluster.MinimumNodes)
	if err != nil {
		return Config{}, err
	}
	config.Cluster.MinimumNodes = minimum
	maximum, err := promptInt(scanner, output, "Maximum nodes", config.Cluster.MaximumNodes)
	if err != nil {
		return Config{}, err
	}
	config.Cluster.MaximumNodes = maximum

	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return config, nil
}

func prompt(scanner *bufio.Scanner, output io.Writer, label, defaultValue string) string {
	if defaultValue == "" {
		fmt.Fprintf(output, "%s: ", label)
	} else {
		fmt.Fprintf(output, "%s [%s]: ", label, defaultValue)
	}
	if !scanner.Scan() {
		return defaultValue
	}
	value := strings.TrimSpace(scanner.Text())
	if value == "" {
		return defaultValue
	}
	return value
}

func promptInt(scanner *bufio.Scanner, output io.Writer, label string, fallback int) (int, error) {
	value := prompt(scanner, output, label, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", strings.ToLower(label), err)
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func EncodeConfig(config Config) ([]byte, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return append(data, '\n'), nil
}

func WriteConfig(path string, config Config) error {
	data, err := EncodeConfig(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("refusing to overwrite existing config %s", path)
		}
		return fmt.Errorf("create config: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	complete = true
	return nil
}
