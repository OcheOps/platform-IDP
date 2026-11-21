package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/OcheOps/platform-IDP/internal/databasejob"
	"github.com/OcheOps/platform-IDP/internal/databaseworker"
	"github.com/OcheOps/platform-IDP/internal/deployworker"
	"github.com/OcheOps/platform-IDP/internal/githubapp"
	"github.com/OcheOps/platform-IDP/internal/kubernetes"
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
	databaseJobWorker, err := newDatabaseJobWorker(installation, os.Getenv)
	if err != nil {
		return err
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
		databaseErr := error(databasejob.ErrNoWork)
		if databaseJobWorker != nil {
			databaseErr = databaseJobWorker.Reconcile(ctx)
		}
		deployErr := deployWorker.Reconcile(ctx)
		runtimeErr := error(nil)
		if runtimeWorkerInstance != nil {
			runtimeErr = runtimeWorkerInstance.Reconcile(ctx)
		}
		if errors.Is(repositoryErr, context.Canceled) || errors.Is(configErr, context.Canceled) || errors.Is(planErr, context.Canceled) || errors.Is(approvalErr, context.Canceled) || errors.Is(applyErr, context.Canceled) || errors.Is(databaseErr, context.Canceled) || errors.Is(deployErr, context.Canceled) || errors.Is(runtimeErr, context.Canceled) {
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
		if databaseErr != nil && !errors.Is(databaseErr, databasejob.ErrNoWork) {
			slog.Error("database Job reconciliation failed", "error", databaseErr)
		}
		if deployErr != nil && !errors.Is(deployErr, deployworker.ErrNoWork) {
			slog.Error("deployment reconciliation failed", "error", deployErr)
		}
		if runtimeErr != nil && !errors.Is(runtimeErr, runtimeworker.ErrNoWork) {
			slog.Error("runtime reconciliation failed", "error", runtimeErr)
		}
		if repositoryErr == nil || configErr == nil || planErr == nil || approvalErr == nil || applyErr == nil || databaseErr == nil || deployErr == nil || runtimeErr == nil {
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

// newDatabaseJobWorker returns nil when KUBERNETES_API_URL is unset; database
// requests then remain in provisioning_database until a cluster is configured.
func newDatabaseJobWorker(installation bootstrap.Config, getenv func(string) string) (*databasejob.Worker, error) {
	apiURL := strings.TrimSpace(getenv("KUBERNETES_API_URL"))
	if apiURL == "" {
		return nil, nil
	}
	tokenFile := strings.TrimSpace(getenv("KUBERNETES_TOKEN_FILE"))
	host := strings.TrimSpace(getenv("DATABASE_HOST"))
	if tokenFile == "" || host == "" {
		return nil, errors.New("KUBERNETES_TOKEN_FILE and DATABASE_HOST are required when KUBERNETES_API_URL is set")
	}
	port := 5432
	if raw := strings.TrimSpace(getenv("DATABASE_PORT")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return nil, errors.New("DATABASE_PORT must be a TCP port")
		}
		port = parsed
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile := strings.TrimSpace(getenv("KUBERNETES_CA_FILE")); caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read Kubernetes CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("KUBERNETES_CA_FILE contains no PEM certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	}
	cluster, err := kubernetes.NewClient(apiURL, &http.Client{Timeout: 30 * time.Second, Transport: transport}, func(context.Context) (string, error) {
		// Re-read on every request so rotated short-lived tokens are picked up.
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", errors.New("read Kubernetes token file")
		}
		token := strings.TrimSpace(string(data))
		if token == "" || len(token) > 16<<10 {
			return "", errors.New("Kubernetes token file is empty or unexpectedly large")
		}
		return token, nil
	})
	if err != nil {
		return nil, err
	}
	masterSecret := fmt.Sprintf("platform/%s/shared/postgres-master", installation.InstallationID)
	store := databaseworker.NewFileStore(valueOrDefault("GENERATED_DIRECTORY", ".platform-data"), host, port, masterSecret)
	return databasejob.New(store, cluster, databasejob.Config{
		Namespace: "platform-system", ServiceAccount: "database-provisioner", Image: strings.TrimSpace(getenv("DATABASE_PROVISIONER_IMAGE")),
		AWSRegion: installation.AWS.Region, VPCCIDR: installation.Network.VPCCIDR,
	})
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
