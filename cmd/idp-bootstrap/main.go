package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/OcheOps/platform-IDP/internal/bootstrap"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	return runWithIO(ctx, args, os.Stdin, os.Stdout)
}

func runWithIO(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: idp-bootstrap <configure|validate|preflight|render|install|postflight> --config <path>")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(output)
	configPath := flags.String("config", "", "path to operator configuration JSON")
	outputDir := flags.String("output", ".idp/generated", "directory for generated non-secret OpenTofu inputs")
	repositoryRoot := flags.String("repository-root", ".", "path to the platform-IDP repository")
	stageName := flags.String("stage", "", "installation stage: bootstrap-state, foundation, or addons")
	apply := flags.Bool("apply", false, "apply the saved plan for the selected installation stage")
	approvedDigest := flags.String("approve", "", "full SHA-256 digest of the reviewed saved plan")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if args[0] == "configure" {
		if _, err := os.Stat(*configPath); err == nil {
			return fmt.Errorf("refusing to overwrite existing config %s", *configPath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect config target: %w", err)
		}
		config, err := bootstrap.Configure(input, output)
		if err != nil {
			return err
		}
		if err := bootstrap.WriteConfig(*configPath, config); err != nil {
			return err
		}
		fmt.Fprintln(output, "wrote non-secret configuration to", *configPath)
		fmt.Fprintf(output, "next: idp-bootstrap preflight --config %s\n", *configPath)
		return nil
	}
	config, err := bootstrap.ReadConfig(*configPath)
	if err != nil {
		return err
	}

	switch args[0] {
	case "validate":
		fmt.Fprintln(output, "configuration is valid")
		return nil
	case "preflight":
		results := bootstrap.RunPreflight(ctx, bootstrap.ExecRunner{}, config)
		for _, result := range results {
			status := "PASS"
			if !result.Success {
				status = "FAIL"
			}
			fmt.Fprintf(output, "%-4s %-14s %s\n", status, result.Name, result.Detail)
		}
		if !bootstrap.PreflightPassed(results) {
			return fmt.Errorf("preflight checks failed")
		}
		return nil
	case "render":
		if err := bootstrap.WriteGeneratedFiles(*outputDir, config); err != nil {
			return err
		}
		fmt.Fprintln(output, "generated OpenTofu inputs in", *outputDir)
		return nil
	case "install":
		if *stageName == "" {
			return fmt.Errorf("--stage is required for install")
		}
		results := bootstrap.RunPreflight(ctx, bootstrap.ExecRunner{}, config)
		for _, result := range results {
			status := "PASS"
			if !result.Success {
				status = "FAIL"
			}
			fmt.Fprintf(output, "%-4s %s\n", status, result.Name)
		}
		if !bootstrap.PreflightPassed(results) {
			return fmt.Errorf("preflight checks failed; fix failed checks and rerun the install command")
		}
		root, err := filepath.Abs(*repositoryRoot)
		if err != nil {
			return fmt.Errorf("resolve repository root: %w", err)
		}
		generated, err := filepath.Abs(*outputDir)
		if err != nil {
			return fmt.Errorf("resolve generated directory: %w", err)
		}
		stage, err := bootstrap.InstallationStage(*stageName, root, generated)
		if err != nil {
			return err
		}
		if *apply {
			if *stageName == "addons" {
				if err := bootstrap.PrepareAddons(ctx, bootstrap.ExecRunner{}, config); err != nil {
					return err
				}
			}
			if err := bootstrap.ApplyInstallationStage(ctx, bootstrap.ExecRunner{}, stage, *approvedDigest, output); err != nil {
				return err
			}
			if *stageName == "addons" {
				return bootstrap.RunPostflight(ctx, bootstrap.ExecRunner{}, config, output)
			}
			return nil
		}
		if *approvedDigest != "" {
			return fmt.Errorf("--approve is only valid together with --apply")
		}
		if err := bootstrap.WriteGeneratedFiles(*outputDir, config); err != nil {
			return err
		}
		if *stageName == "addons" {
			if err := bootstrap.PrepareAddons(ctx, bootstrap.ExecRunner{}, config); err != nil {
				return err
			}
		}
		_, err = bootstrap.PlanInstallationStage(ctx, bootstrap.ExecRunner{}, stage, output)
		return err
	case "postflight":
		if err := bootstrap.PrepareAddons(ctx, bootstrap.ExecRunner{}, config); err != nil {
			return err
		}
		return bootstrap.RunPostflight(ctx, bootstrap.ExecRunner{}, config, output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
