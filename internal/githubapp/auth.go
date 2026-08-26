package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

type AppConfig struct {
	BaseURL        string
	HTTPClient     *http.Client
	AppID          string
	InstallationID int64
	PrivateKeyPEM  []byte
	Now            func() time.Time
}

type installationTokenSource struct {
	config    AppConfig
	key       *rsa.PrivateKey
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

var requiredPermissions = map[string]string{"administration": "write", "contents": "write", "members": "read"}

func NewInstallationTokenSource(config AppConfig) (TokenSource, error) {
	source, err := newInstallationTokenSource(config)
	if err != nil {
		return nil, err
	}
	return source.Token, nil
}

func newInstallationTokenSource(config AppConfig) (*installationTokenSource, error) {
	if err := validateBaseURL(config.BaseURL); err != nil {
		return nil, err
	}
	appID, appIDError := strconv.ParseInt(strings.TrimSpace(config.AppID), 10, 64)
	if config.HTTPClient == nil || strings.TrimSpace(config.BaseURL) == "" || appIDError != nil || appID <= 0 || config.InstallationID <= 0 {
		return nil, errors.New("GitHub App URL, HTTP client, app ID, and installation ID are required")
	}
	key, err := parsePrivateKey(config.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	source := &installationTokenSource{config: config, key: key}
	return source, nil
}

func (source *installationTokenSource) Token(ctx context.Context) (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	now := source.config.Now().UTC()
	if source.token != "" && now.Add(5*time.Minute).Before(source.expiresAt) {
		return source.token, nil
	}
	jwt, err := source.jwt(now)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{"permissions": requiredPermissions})
	path := fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimRight(source.config.BaseURL, "/"), source.config.InstallationID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+jwt)
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	request.Header.Set("Content-Type", "application/json")
	response, err := source.config.HTTPClient.Do(request)
	if err != nil {
		return "", repositoryworker.Transient(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", repositoryworker.Transient(err)
	}
	if response.StatusCode != http.StatusCreated {
		failure := fmt.Errorf("GitHub installation token endpoint returned %d", response.StatusCode)
		if response.StatusCode == 429 || response.StatusCode >= 500 || (response.StatusCode == 403 && (response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0")) {
			return "", repositoryworker.Transient(failure)
		}
		return "", failure
	}
	var result struct {
		Token       string            `json:"token"`
		ExpiresAt   time.Time         `json:"expires_at"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}
	if result.Token == "" || result.ExpiresAt.Before(now.Add(5*time.Minute)) {
		return "", errors.New("GitHub returned an invalid installation token")
	}
	for permission, required := range requiredPermissions {
		if result.Permissions[permission] != required {
			return "", fmt.Errorf("GitHub App installation lacks required %s:%s permission", permission, required)
		}
	}
	source.token, source.expiresAt = result.Token, result.ExpiresAt
	return source.token, nil
}

// ValidateInstallation verifies that the configured installation belongs to the
// expected organization, covers future repositories, and has least privilege.
func ValidateInstallation(ctx context.Context, config AppConfig, expectedOrganization string) error {
	source, err := newInstallationTokenSource(config)
	if err != nil {
		return err
	}
	jwt, err := source.jwt(source.config.Now().UTC())
	if err != nil {
		return err
	}
	var app struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Public bool `json:"public"`
	}
	if err := getWithJWT(ctx, config, jwt, "/app", &app); err != nil {
		return fmt.Errorf("read GitHub App metadata: %w", err)
	}
	if app.Public || !strings.EqualFold(app.Owner.Login, strings.TrimSpace(expectedOrganization)) {
		return fmt.Errorf("GitHub App must be private and owned by organization %q", expectedOrganization)
	}
	var installation struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
		TargetType          string            `json:"target_type"`
		RepositorySelection string            `json:"repository_selection"`
		Permissions         map[string]string `json:"permissions"`
		SuspendedAt         *time.Time        `json:"suspended_at"`
	}
	installationPath := fmt.Sprintf("/app/installations/%d", config.InstallationID)
	if err := getWithJWT(ctx, config, jwt, installationPath, &installation); err != nil {
		return fmt.Errorf("read GitHub installation: %w", err)
	}
	if installation.SuspendedAt != nil {
		return errors.New("GitHub App installation is suspended")
	}
	if installation.TargetType != "Organization" || !strings.EqualFold(installation.Account.Login, strings.TrimSpace(expectedOrganization)) {
		return fmt.Errorf("GitHub App installation does not belong to organization %q", expectedOrganization)
	}
	if installation.RepositorySelection != "all" {
		return errors.New("GitHub App must be installed for all repositories so it can access newly created repositories")
	}
	for permission, required := range requiredPermissions {
		if installation.Permissions[permission] != required {
			return fmt.Errorf("GitHub App installation lacks required %s:%s permission", permission, required)
		}
	}
	for permission, access := range installation.Permissions {
		if permission == "metadata" && access == "read" {
			continue
		}
		if requiredPermissions[permission] != access {
			return fmt.Errorf("GitHub App installation has unexpected %s:%s permission", permission, access)
		}
	}
	_, err = source.Token(ctx)
	return err
}

func getWithJWT(ctx context.Context, config AppConfig, jwt, path string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.BaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+jwt)
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return repositoryworker.Transient(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return repositoryworker.Transient(err)
	}
	if response.StatusCode != http.StatusOK {
		failure := fmt.Errorf("GitHub endpoint %s returned %d", path, response.StatusCode)
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 || (response.StatusCode == http.StatusForbidden && (response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0")) {
			return repositoryworker.Transient(failure)
		}
		return failure
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

func (source *installationTokenSource) jwt(now time.Time) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-60 * time.Second).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": source.config.AppID})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, source.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub App private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("GitHub App private key is not RSA PKCS#1 or PKCS#8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App private key is not RSA")
	}
	return key, nil
}
