package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type CheckResult struct {
	Name    string
	Detail  string
	Success bool
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func RunPreflight(ctx context.Context, runner CommandRunner, config Config) []CheckResult {
	checks := []struct {
		name string
		cmd  string
		args []string
	}{
		{"OpenTofu", "tofu", []string{"version"}},
		{"AWS CLI", "aws", []string{"--version"}},
		{"AWS identity", "aws", awsIdentityArgs(config)},
		{"GitHub CLI", "gh", []string{"auth", "status", "-h", "github.com"}},
		{"GitHub organization", "gh", []string{"api", "orgs/" + config.GitHub.Organization, "--jq", ".login"}},
		{"kubectl", "kubectl", []string{"version", "--client"}},
		{"Helm", "helm", []string{"version", "--short"}},
	}
	results := make([]CheckResult, 0, len(checks))
	for _, check := range checks {
		output, err := runner.Run(ctx, check.cmd, check.args...)
		if check.name == "AWS identity" && err == nil {
			var identity struct {
				Account string `json:"Account"`
			}
			if decodeErr := json.Unmarshal(output, &identity); decodeErr != nil || identity.Account != config.AWS.AccountID {
				err = fmt.Errorf("authenticated AWS account does not match configured account %s", config.AWS.AccountID)
			}
		}
		detail := strings.TrimSpace(string(output))
		if err != nil {
			detail = fmt.Sprintf("%s: %v", detail, err)
		}
		results = append(results, CheckResult{Name: check.name, Detail: detail, Success: err == nil})
	}
	if config.DNS.Provider == "route53" {
		args := []string{"route53", "get-hosted-zone", "--id", config.DNS.ZoneID, "--region", config.AWS.Region}
		if config.AWS.Profile != "" {
			args = append(args, "--profile", config.AWS.Profile)
		}
		output, err := runner.Run(ctx, "aws", args...)
		results = append(results, CheckResult{Name: "Route 53 zone", Detail: strings.TrimSpace(string(output)), Success: err == nil})
	}
	return results
}

func awsIdentityArgs(config Config) []string {
	args := []string{"sts", "get-caller-identity", "--output", "json", "--region", config.AWS.Region}
	if config.AWS.Profile != "" {
		args = append(args, "--profile", config.AWS.Profile)
	}
	return args
}

func PreflightPassed(results []CheckResult) bool {
	for _, result := range results {
		if !result.Success {
			return false
		}
	}
	return true
}
