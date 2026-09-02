package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/OcheOps/platform-IDP/internal/applyworker"
	"github.com/OcheOps/platform-IDP/internal/approvalworker"
	"github.com/OcheOps/platform-IDP/internal/argocd"
	"github.com/OcheOps/platform-IDP/internal/bootstrap"
	"github.com/OcheOps/platform-IDP/internal/configworker"
	"github.com/OcheOps/platform-IDP/internal/deployworker"
	"github.com/OcheOps/platform-IDP/internal/githubapp"
	"github.com/OcheOps/platform-IDP/internal/planworker"
	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
	"github.com/OcheOps/platform-IDP/internal/runtimeworker"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("repository worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	preflight := len(args) == 1 && args[0] == "--preflight"
	if len(args) > 0 && !preflight {
		return errors.New("usage: repository-worker [--preflight]")
	}
	installation, err := bootstrap.ReadConfig(valueOrDefault("INSTALLATION_CONFIG", ".idp/config.json"))
	if err != nil {
		return fmt.Errorf("read installation configuration: %w", err)
	}
	appID := strings.TrimSpace(os.Getenv("GITHUB_APP_ID"))
	installationID, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALLATION_ID")), 10, 64)
	if err != nil || installationID <= 0 {
		return errors.New("GITHUB_APP_INSTALLATION_ID must be a positive integer")
	}
	privateKey, err := readPrivateKey(os.Getenv("GITHUB_APP_PRIVATE_KEY_FILE"))
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	apiURL := valueOrDefault("GITHUB_API_URL", "https://api.github.com")
	tokens, err := githubapp.NewInstallationTokenSource(githubapp.AppConfig{BaseURL: apiURL, HTTPClient: httpClient, AppID: appID, InstallationID: installationID, PrivateKeyPEM: privateKey})
	if err != nil {
		return err
	}
	if preflight {
		config := githubapp.AppConfig{BaseURL: apiURL, HTTPClient: httpClient, AppID: appID, InstallationID: installationID, PrivateKeyPEM: privateKey}
		if err := githubapp.ValidateInstallation(context.Background(), config, installation.GitHub.Organization); err != nil {
			return fmt.Errorf("GitHub App preflight: %w", err)
		}
		fmt.Println("GitHub App organization, repository scope, credentials, and permissions are valid")
		return nil
	}
	github, err := githubapp.NewClient(apiURL, httpClient, tokens)
	if err != nil {
		return err
	}
	store := repositoryworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization)
	worker := repositoryworker.New(store, github)
	configStore := configworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization, valueOrDefault("PLATFORM_CONFIG_REPOSITORY", "platform-config"), configworker.Platform{
		InstallationID: installation.InstallationID, AWSAccountID: installation.AWS.AccountID, AWSRegion: installation.AWS.Region,
		AWSRoleARN: os.Getenv("GITHUB_ACTIONS_ROLE_ARN"), StateBucket: fmt.Sprintf("%s-%s-%s-tofu-state", installation.InstallationID, installation.AWS.AccountID, installation.AWS.Region),
		LockTable: installation.InstallationID + "-tofu-locks", BaseDomain: installation.DNS.Domain,
	})
	configWorker := configworker.New(configStore, github)
	planStore := planworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization, valueOrDefault("PLATFORM_CONFIG_REPOSITORY", "platform-config"))
	planWorker := planworker.New(planStore, github)
	approvalStore := approvalworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization, valueOrDefault("PLATFORM_CONFIG_REPOSITORY", "platform-config"), installation.GitHub.EngineeringOwnerTeam, installation.GitHub.PlatformOwnerTeam)
	approvalWorker := approvalworker.New(approvalStore, github)
	applyStore := applyworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization, valueOrDefault("PLATFORM_CONFIG_REPOSITORY", "platform-config"), installation.InstallationID, installation.AWS.AccountID, installation.AWS.Region)
	applyWorker := applyworker.New(applyStore, github)
	deployStore := deployworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), installation.GitHub.Organization, valueOrDefault("PLATFORM_CONFIG_REPOSITORY", "platform-config"), installation.DNS.Domain, installation.AWS.AccountID, installation.AWS.Region)
	deployWorker := deployworker.New(deployStore, github)
	var runtimeWorkerInstance *runtimeworker.Worker
	if argoURL := strings.TrimSpace(os.Getenv("ARGOCD_URL")); argoURL != "" {
		argo, err := argocd.NewClient(argoURL, httpClient, func(context.Context) (string, error) {
			token := strings.TrimSpace(os.Getenv("ARGOCD_AUTH_TOKEN"))
			if token == "" {
				return "", errors.New("ARGOCD_AUTH_TOKEN is required when ARGOCD_URL is set")
			}
			return token, nil
		})
		if err != nil {
			return err
		}
		runtimeWorkerInstance = runtimeworker.New(deployStore, github, argo)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	interval := 30 * time.Second
	for {
		repositoryErr := worker.Reconcile(ctx)
		configErr := configWorker.Reconcile(ctx)
		planErr := planWorker.Reconcile(ctx)
		approvalErr := approvalWorker.Reconcile(ctx)
		applyErr := applyWorker.Reconcile(ctx)
		deployErr := deployWorker.Reconcile(ctx)
		runtimeErr := error(nil)
		if runtimeWorkerInstance != nil {
			runtimeErr = runtimeWorkerInstance.Reconcile(ctx)
		}
		if errors.Is(repositoryErr, context.Canceled) || errors.Is(configErr, context.Canceled) || errors.Is(planErr, context.Canceled) || errors.Is(approvalErr, context.Canceled) || errors.Is(applyErr, context.Canceled) || errors.Is(deployErr, context.Canceled) || errors.Is(runtimeErr, context.Canceled) {
			return nil
		}
		if repositoryErr != nil && !errors.Is(repositoryErr, repositoryworker.ErrNoWork) {
			slog.Error("repository reconciliation failed", "error", repositoryErr)
		}
		if configErr != nil && !errors.Is(configErr, configworker.ErrNoWork) {
			slog.Error("platform-config reconciliation failed", "error", configErr)
		}
		if planErr != nil && !errors.Is(planErr, planworker.ErrNoWork) {
			slog.Error("plan reconciliation failed", "error", planErr)
		}
		if approvalErr != nil && !errors.Is(approvalErr, approvalworker.ErrNoWork) {
			slog.Error("approval reconciliation failed", "error", approvalErr)
		}
		if applyErr != nil && !errors.Is(applyErr, applyworker.ErrNoWork) {
			slog.Error("apply reconciliation failed", "error", applyErr)
		}
		if deployErr != nil && !errors.Is(deployErr, deployworker.ErrNoWork) {
			slog.Error("deployment reconciliation failed", "error", deployErr)
		}
		if runtimeErr != nil && !errors.Is(runtimeErr, runtimeworker.ErrNoWork) {
			slog.Error("runtime reconciliation failed", "error", runtimeErr)
		}
		if repositoryErr == nil || configErr == nil || planErr == nil || approvalErr == nil || applyErr == nil || deployErr == nil || runtimeErr == nil {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func readPrivateKey(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("GITHUB_APP_PRIVATE_KEY_FILE is required")
	}
	descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open GitHub App private key: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = syscall.Close(descriptor)
		return nil, errors.New("open GitHub App private key")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat GitHub App private key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("GitHub App private key must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("GitHub App private key must not be accessible by group or others")
	}
	if info.Size() > 64<<10 {
		return nil, errors.New("GitHub App private key is unexpectedly large")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub App private key: %w", err)
	}
	if len(data) > 64<<10 {
		return nil, errors.New("GitHub App private key is unexpectedly large")
	}
	return data, nil
}
func valueOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
