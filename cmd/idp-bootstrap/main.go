package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/OcheOps/platform-IDP/internal/bootstrap"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: idp-bootstrap <validate|preflight|render> --config <path>")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	configPath := flags.String("config", "", "path to operator configuration JSON")
	outputDir := flags.String("output", ".idp/generated", "directory for generated non-secret OpenTofu inputs")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	config, err := bootstrap.ReadConfig(*configPath)
	if err != nil {
		return err
	}

	switch args[0] {
	case "validate":
		fmt.Println("configuration is valid")
		return nil
	case "preflight":
		results := bootstrap.RunPreflight(ctx, bootstrap.ExecRunner{}, config)
		for _, result := range results {
			status := "PASS"
			if !result.Success {
				status = "FAIL"
			}
			fmt.Printf("%-4s %-14s %s\n", status, result.Name, result.Detail)
		}
		if !bootstrap.PreflightPassed(results) {
			return fmt.Errorf("preflight checks failed")
		}
		return nil
	case "render":
		if err := bootstrap.WriteGeneratedFiles(*outputDir, config); err != nil {
			return err
		}
		fmt.Println("generated OpenTofu inputs in", *outputDir)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
