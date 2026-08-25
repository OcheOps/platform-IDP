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

func NewInstallationTokenSource(config AppConfig) (TokenSource, error) {
	if err := validateBaseURL(config.BaseURL); err != nil {
		return nil, err
	}
	if config.HTTPClient == nil || strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.AppID) == "" || config.InstallationID <= 0 {
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
	return source.Token, nil
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
	body, _ := json.Marshal(map[string]any{"permissions": map[string]string{"administration": "write", "contents": "write", "members": "read"}})
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
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("decode installation token: %w", err)
	}
	if result.Token == "" || result.ExpiresAt.Before(now.Add(5*time.Minute)) {
		return "", errors.New("GitHub returned an invalid installation token")
	}
	source.token, source.expiresAt = result.Token, result.ExpiresAt
	return source.token, nil
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
