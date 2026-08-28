package githubapp_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OcheOps/platform-IDP/internal/githubapp"
)

func TestInstallationTokenSourceSignsJWTAndCachesShortLivedToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	requests := 0
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/app/installations/99/access_tokens" {
			t.Errorf("path=%s", r.URL.Path)
		}
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			t.Fatalf("invalid JWT")
		}
		claimsData, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(claimsData, &claims)
		if claims["iss"] != "123" {
			t.Errorf("issuer=%v", claims["iss"])
		}
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("invalid JWT signature: %v", err)
		}
		writeJSON(w, 201, map[string]any{"token": "ghs_example_token", "expires_at": now.Add(time.Hour).Format(time.RFC3339), "permissions": expectedPermissions()})
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	source, err := githubapp.NewInstallationTokenSource(githubapp.AppConfig{BaseURL: server.URL, HTTPClient: server.Client(), AppID: "123", InstallationID: 99, PrivateKeyPEM: privatePEM, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		token, err := source(context.Background())
		if err != nil || token != "ghs_example_token" {
			t.Fatalf("token=%q err=%v", token, err)
		}
	}
	if requests != 1 {
		t.Fatalf("token requests=%d, want 1", requests)
	}
}

func TestValidateInstallationChecksOrganizationScopeAndPermissions(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP sockets unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			writeJSON(w, http.StatusOK, map[string]any{"owner": map[string]string{"login": "OcheOps"}, "public": false})
		case "/app/installations/99":
			writeJSON(w, http.StatusOK, map[string]any{
				"account":              map[string]string{"login": "OcheOps"},
				"target_type":          "Organization",
				"repository_selection": "all",
				"permissions":          withMetadata(expectedPermissions()),
				"suspended_at":         nil,
			})
		case "/app/installations/99/access_tokens":
			writeJSON(w, http.StatusCreated, map[string]any{"token": "ghs_example_token", "expires_at": now.Add(time.Hour).Format(time.RFC3339), "permissions": expectedPermissions()})
		default:
			http.NotFound(w, r)
		}
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	config := githubapp.AppConfig{BaseURL: server.URL, HTTPClient: server.Client(), AppID: "123", InstallationID: 99, PrivateKeyPEM: privatePEM, Now: func() time.Time { return now }}
	if err := githubapp.ValidateInstallation(context.Background(), config, "ocheops"); err != nil {
		t.Fatal(err)
	}
}

func expectedPermissions() map[string]string {
	return map[string]string{"administration": "write", "checks": "read", "contents": "write", "members": "read", "pull_requests": "read", "statuses": "read", "workflows": "write"}
}
func withMetadata(value map[string]string) map[string]string {
	result := map[string]string{"metadata": "read"}
	for key, access := range value {
		result[key] = access
	}
	return result
}

func TestInstallationTokenSourceRejectsNonNumericAppID(t *testing.T) {
	_, err := githubapp.NewInstallationTokenSource(githubapp.AppConfig{BaseURL: "https://api.github.com", HTTPClient: http.DefaultClient, AppID: "client-id", InstallationID: 99})
	if err == nil {
		t.Fatal("expected invalid App ID to be rejected")
	}
}
