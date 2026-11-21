package kubernetes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const FieldManager = "platform-idp-control-plane"

// Client is a minimal Kubernetes API client for the control plane. It only
// supports server-side apply and reads so every write is declarative and
// idempotent under retries.
type Client struct {
	baseURL string
	http    *http.Client
	token   func(context.Context) (string, error)
}

func NewClient(baseURL string, httpClient *http.Client, token func(context.Context) (string, error)) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("Kubernetes API URL must be an HTTPS origin without credentials")
	}
	if httpClient == nil || token == nil {
		return nil, errors.New("Kubernetes HTTP client and token source are required")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, token: token}, nil
}

// Apply creates or converges an object with server-side apply. Conflicting
// field ownership and immutable-field changes are returned as errors rather
// than forced.
func (client *Client) Apply(ctx context.Context, path string, object any) error {
	if err := validPath(path); err != nil {
		return err
	}
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	query := url.Values{"fieldManager": {FieldManager}}
	status, _, err := client.do(ctx, http.MethodPatch, path+"?"+query.Encode(), body)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("Kubernetes apply %s returned %d", path, status)
	}
	return nil
}

// Get decodes an object into out and reports whether it exists.
func (client *Client) Get(ctx context.Context, path string, out any) (bool, error) {
	if err := validPath(path); err != nil {
		return false, err
	}
	status, data, err := client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("Kubernetes read %s returned %d", path, status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("decode Kubernetes object %s: %w", path, err)
	}
	return true, nil
}

func (client *Client) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	token, err := client.token(ctx)
	if err != nil {
		return 0, nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/apply-patch+yaml")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, data, nil
}

func validPath(path string) error {
	if !strings.HasPrefix(path, "/api") || strings.ContainsAny(path, "?#") || strings.Contains(path, "..") {
		return errors.New("Kubernetes API path is invalid")
	}
	return nil
}
