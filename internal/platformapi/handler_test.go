package platformapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/platformapi"
)

func TestAuthenticatedDeveloperCanCreateGoService(t *testing.T) {
	root := repositoryRoot(t)
	generated := t.TempDir()
	handler, err := platformapi.New(platformapi.Config{
		SchemaDirectory:      filepath.Join(root, "contracts", "schemas"),
		TemplateDirectory:    filepath.Join(root, "templates", "go-chi-api"),
		GeneratedDirectory:   generated,
		GitHubOrganization:   "acme",
		AWSRegion:            "eu-west-1",
		GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/platform-github-actions",
		Authenticate:         func(*http.Request) (string, error) { return "ocheops", nil },
		Authorize:            func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	var result struct {
		RequestID       string `json:"requestId"`
		RequestState    string `json:"requestState"`
		RepositoryState string `json:"repositoryState"`
		ServiceName     string `json:"serviceName"`
		CostEstimate    struct {
			Digest             string `json:"digest"`
			PriceSource        string `json:"priceSource"`
			IncrementalService struct {
				AmountCents int64 `json:"amountCents"`
			} `json:"incrementalService"`
		} `json:"costEstimate"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "f4d26f61-ef7d-4bd9-9568-ae621d91187f" || result.RequestState != "draft" || result.RepositoryState != "repository_creating" || result.ServiceName != "orders-api" {
		t.Fatalf("unexpected response: %#v", result)
	}
	if result.CostEstimate.Digest == "" || result.CostEstimate.PriceSource != "versioned-fallback" || result.CostEstimate.IncrementalService.AmountCents <= 0 {
		t.Fatalf("missing cost estimate: %#v", result.CostEstimate)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/v1/service-requests", nil))
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte(`"serviceName":"orders-api"`)) {
		t.Fatalf("catalogue response = %d: %s", list.Code, list.Body.String())
	}
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/v1/service-requests/"+result.RequestID, nil))
	if detail.Code != http.StatusOK || !bytes.Contains(detail.Body.Bytes(), []byte(`"requestId":"`+result.RequestID+`"`)) {
		t.Fatalf("detail response = %d: %s", detail.Code, detail.Body.String())
	}
	for _, path := range []string{
		filepath.Join(generated, "repositories", "orders-api", "go.mod"),
		filepath.Join(generated, "requests", result.RequestID+".json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s: %v", path, err)
		}
	}
	module, err := os.ReadFile(filepath.Join(generated, "repositories", "orders-api", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(module, []byte("module github.com/acme/orders-api")) {
		t.Fatalf("rendered module path is wrong: %s", module)
	}
	record, err := os.ReadFile(filepath.Join(generated, "requests", result.RequestID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range [][]byte{[]byte(`"capacity"`), []byte(`"preview"`), []byte(`"actor": "ocheops"`), []byte(`"action": "request.created"`), []byte(`"costEstimate"`), []byte(`"versioned-fallback"`)} {
		if !bytes.Contains(record, expected) {
			t.Errorf("persisted workflow record missing %s", expected)
		}
	}
	var interrupted map[string]any
	if err := json.Unmarshal(record, &interrupted); err != nil {
		t.Fatal(err)
	}
	interrupted["repositoryState"] = "repository_rendering"
	interruptedRecord, err := json.Marshal(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generated, "requests", result.RequestID+".json"), interruptedRecord, 0o600); err != nil {
		t.Fatal(err)
	}

	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest())))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200: %s", retry.Code, retry.Body.String())
	}
}

func TestServiceRequestBoundaryRejectsInvalidOrUnauthorizedRequests(t *testing.T) {
	root := repositoryRoot(t)
	newHandler := func(actor string, authErr error) http.Handler {
		handler, err := platformapi.New(platformapi.Config{
			SchemaDirectory: filepath.Join(root, "contracts", "schemas"), TemplateDirectory: filepath.Join(root, "templates", "go-chi-api"), GeneratedDirectory: t.TempDir(),
			GitHubOrganization: "acme", AWSRegion: "eu-west-1", GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/platform-github-actions",
			Authenticate: func(*http.Request) (string, error) { return actor, authErr },
			Authorize:    func(string, string) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}

	t.Run("unauthenticated", func(t *testing.T) {
		response := httptest.NewRecorder()
		newHandler("", platformapi.ErrUnauthenticated).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest())))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.Code)
		}
	})
	t.Run("requester does not match actor", func(t *testing.T) {
		response := httptest.NewRecorder()
		newHandler("someone-else", nil).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest())))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", response.Code)
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		response := httptest.NewRecorder()
		body := bytes.Replace(validRequest(), []byte(`"environment":"staging"`), []byte(`"environment":"staging","unsafe":true`), 1)
		newHandler("ocheops", nil).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
		}
	})
}

func TestRequestIDCannotBeReusedForDifferentContent(t *testing.T) {
	root := repositoryRoot(t)
	handler, err := platformapi.New(platformapi.Config{
		SchemaDirectory: filepath.Join(root, "contracts", "schemas"), TemplateDirectory: filepath.Join(root, "templates", "go-chi-api"), GeneratedDirectory: t.TempDir(),
		GitHubOrganization: "acme", AWSRegion: "eu-west-1", GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/platform-github-actions",
		Authenticate: func(*http.Request) (string, error) { return "ocheops", nil },
		Authorize:    func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest())))
	changed := bytes.Replace(validRequest(), []byte("Order capture API"), []byte("Different API"), 1)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(changed)))
	if second.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", second.Code, second.Body.String())
	}
}

func TestRenderFailureIsPersistedAsRepositoryFailed(t *testing.T) {
	root := repositoryRoot(t)
	generated := t.TempDir()
	if err := os.MkdirAll(filepath.Join(generated, "repositories", "orders-api"), 0o750); err != nil {
		t.Fatal(err)
	}
	handler, err := platformapi.New(platformapi.Config{
		SchemaDirectory: filepath.Join(root, "contracts", "schemas"), TemplateDirectory: filepath.Join(root, "templates", "go-chi-api"), GeneratedDirectory: generated,
		GitHubOrganization: "acme", AWSRegion: "eu-west-1", GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/platform-github-actions",
		Authenticate: func(*http.Request) (string, error) { return "ocheops", nil }, Authorize: func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/service-requests", bytes.NewReader(validRequest())))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	record, err := os.ReadFile(filepath.Join(generated, "requests", "f4d26f61-ef7d-4bd9-9568-ae621d91187f.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range [][]byte{[]byte(`"repositoryState": "repository_failed"`), []byte(`"failureCode": "repository_render_failed"`), []byte(`"action": "repository.failed"`)} {
		if !bytes.Contains(record, expected) {
			t.Errorf("failed workflow record missing %s", expected)
		}
	}
}

func TestHealthEndpointDoesNotRequireAuthentication(t *testing.T) {
	root := repositoryRoot(t)
	handler, err := platformapi.New(platformapi.Config{
		SchemaDirectory: filepath.Join(root, "contracts", "schemas"), TemplateDirectory: filepath.Join(root, "templates", "go-chi-api"), GeneratedDirectory: t.TempDir(),
		GitHubOrganization: "acme", AWSRegion: "eu-west-1", GitHubActionsRoleARN: "arn:aws:iam::123456789012:role/platform-github-actions",
		Authenticate: func(*http.Request) (string, error) { return "", platformapi.ErrUnauthenticated },
		Authorize:    func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func validRequest() []byte {
	return []byte(`{"apiVersion":"platform.idp/v1alpha1","kind":"ServiceRequest","metadata":{"name":"orders-api","owner":"orders-team","requester":"ocheops","requestId":"f4d26f61-ef7d-4bd9-9568-ae621d91187f","description":"Order capture API"},"spec":{"template":{"id":"go-chi-api","version":"v0.1.0"},"environment":"staging","capacity":{"cpu":"250m","memory":"256Mi","minReplicas":1,"maxReplicas":3},"database":{"engine":"postgres"},"preview":{"enabled":true,"ttlHours":72}}}`)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
