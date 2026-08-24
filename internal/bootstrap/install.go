package bootstrap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type InstallStage struct {
	Name        string
	Directory   string
	BackendFile string
	Variables   string
	PlanFile    string
}

func InstallationStage(name, repositoryRoot, generatedDir string) (InstallStage, error) {
	stage := InstallStage{Name: name, Directory: filepath.Join(repositoryRoot, "infrastructure", name), PlanFile: filepath.Join(generatedDir, name+".tfplan")}
	switch name {
	case "bootstrap-state":
		stage.Variables = filepath.Join(generatedDir, "bootstrap-state.auto.tfvars.json")
	case "foundation":
		stage.BackendFile = filepath.Join(generatedDir, "backend.hcl")
		stage.Variables = filepath.Join(generatedDir, "foundation.auto.tfvars.json")
	case "addons":
		stage.BackendFile = filepath.Join(generatedDir, "addons-backend.hcl")
		stage.Variables = filepath.Join(generatedDir, "addons.auto.tfvars.json")
	default:
		return InstallStage{}, fmt.Errorf("unknown installation stage %q", name)
	}
	return stage, nil
}

func PlanInstallationStage(ctx context.Context, runner CommandRunner, stage InstallStage, output io.Writer) (string, error) {
	if err := preparePlanPath(stage.PlanFile); err != nil {
		return "", err
	}
	initArgs := []string{"-chdir=" + stage.Directory, "init", "-input=false"}
	if stage.BackendFile != "" {
		initArgs = append(initArgs, "-backend-config="+stage.BackendFile)
	}
	if _, err := runner.Run(ctx, "tofu", initArgs...); err != nil {
		return "", fmt.Errorf("initialize %s failed; inspect OpenTofu locally, fix the problem, and rerun plan: %w", stage.Name, err)
	}
	planArgs := []string{"-chdir=" + stage.Directory, "plan", "-input=false", "-var-file=" + stage.Variables, "-out=" + stage.PlanFile}
	if _, err := runner.Run(ctx, "tofu", planArgs...); err != nil {
		_ = os.Remove(stage.PlanFile)
		return "", fmt.Errorf("plan %s failed; inspect OpenTofu locally, fix the problem, and rerun plan: %w", stage.Name, err)
	}
	if err := os.Chmod(stage.PlanFile, 0o600); err != nil {
		_ = os.Remove(stage.PlanFile)
		return "", fmt.Errorf("protect %s plan: %w", stage.Name, err)
	}
	digest, err := PlanDigest(stage.PlanFile)
	if err != nil {
		_ = os.Remove(stage.PlanFile)
		return "", err
	}
	fmt.Fprintf(output, "planned %s\nreview: tofu -chdir=%s show %s\nplan digest: %s\n", stage.Name, stage.Directory, stage.PlanFile, digest)
	return digest, nil
}

func preparePlanPath(planPath string) error {
	directory := filepath.Dir(planPath)
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect protected plan directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("protected plan directory must be a real directory: %s", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect plan directory: %w", err)
	}
	if info, err := os.Lstat(planPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing unsafe existing plan path %s", planPath)
		}
		if err := os.Remove(planPath); err != nil {
			return fmt.Errorf("replace previous saved plan: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect saved plan: %w", err)
	}
	return nil
}

func ApplyInstallationStage(ctx context.Context, runner CommandRunner, stage InstallStage, approvedDigest string, output io.Writer) error {
	digest, err := PlanDigest(stage.PlanFile)
	if err != nil {
		return err
	}
	if approvedDigest == "" || approvedDigest != digest {
		return fmt.Errorf("plan approval digest does not match %s; review the saved plan and pass its full digest", stage.Name)
	}
	if _, err := runner.Run(ctx, "tofu", "-chdir="+stage.Directory, "apply", "-input=false", stage.PlanFile); err != nil {
		return fmt.Errorf("apply %s failed; the protected plan remains available for inspection and retry: %w", stage.Name, err)
	}
	if err := os.Remove(stage.PlanFile); err != nil {
		return fmt.Errorf("%s applied but secure plan cleanup failed: %w", stage.Name, err)
	}
	fmt.Fprintf(output, "applied %s and removed its saved plan\n", stage.Name)
	return nil
}

func PlanDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read protected plan %s: %w", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func PrepareAddons(ctx context.Context, runner CommandRunner, config Config) error {
	if _, err := runner.Run(ctx, "aws", awsKubeconfigArgs(config)...); err != nil {
		return fmt.Errorf("configure kubectl failed; verify the foundation stage and AWS identity: %w", err)
	}
	if config.DNS.Provider != "cloudflare" {
		return nil
	}
	if _, err := runner.Run(ctx, "kubectl", "-n", "external-dns", "get", "secret", config.DNS.CloudflareTokenSecretName); err != nil {
		return fmt.Errorf("Cloudflare token Secret %q is not ready; create the external-dns namespace and Secret using docs/phase-1/bootstrap.md, then rerun the add-ons plan", config.DNS.CloudflareTokenSecretName)
	}
	return nil
}

func RunPostflight(ctx context.Context, runner CommandRunner, config Config, output io.Writer) error {
	checks := []struct {
		name    string
		command string
		args    []string
	}{
		{name: "EKS nodes", command: "kubectl", args: []string{"wait", "--for=condition=Ready", "nodes", "--all", "--timeout=10m"}},
		{name: "ingress controller", command: "kubectl", args: readyPods("ingress-nginx", "ingress-nginx")},
		{name: "certificate manager", command: "kubectl", args: readyPods("cert-manager", "cert-manager")},
		{name: "external secrets", command: "kubectl", args: readyPods("external-secrets", "external-secrets")},
		{name: "external DNS", command: "kubectl", args: readyPods("external-dns", "external-dns")},
		{name: "Argo CD", command: "kubectl", args: readyPods("argocd", "argo-cd")},
		{name: "Loki", command: "kubectl", args: readyPods("observability", "loki")},
		{name: "Alloy", command: "kubectl", args: readyPods("observability", "alloy")},
		{name: "Prometheus and Grafana", command: "kubectl", args: readyPods("observability", "kube-prometheus-stack")},
		{name: "Argo CD HTTPS", command: "curl", args: []string{"--fail", "--silent", "--show-error", "--head", "--max-time", "15", "https://argocd." + config.DNS.Domain}},
	}
	for _, check := range checks {
		if _, err := runner.Run(ctx, check.command, check.args...); err != nil {
			return fmt.Errorf("postflight check %q failed; no provider output was displayed—inspect that component and rerun postflight: %w", check.name, err)
		}
		fmt.Fprintf(output, "PASS %s\n", check.name)
	}
	return nil
}

func readyPods(namespace, release string) []string {
	return []string{"-n", namespace, "wait", "--for=condition=Ready", "pods", "--selector=app.kubernetes.io/instance=" + release, "--timeout=10m"}
}

func awsKubeconfigArgs(config Config) []string {
	args := []string{"eks", "update-kubeconfig", "--name", config.InstallationID + "-eks", "--region", config.AWS.Region}
	if config.AWS.Profile != "" {
		args = append(args, "--profile", config.AWS.Profile)
	}
	return args
}
