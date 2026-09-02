package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	Pending  = "pending"
	Healthy  = "healthy"
	Degraded = "degraded"
)

type Observation struct {
	State, Revision, Message string
}

type Client struct {
	baseURL string
	http    *http.Client
	token   func(context.Context) (string, error)
}

func NewClient(baseURL string, httpClient *http.Client, token func(context.Context) (string, error)) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("Argo CD URL must be HTTPS and contain no credentials")
	}
	if httpClient == nil || token == nil {
		return nil, errors.New("Argo CD HTTP client and token source are required")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, token: token}, nil
}

func (client *Client) Observe(ctx context.Context, application string) (Observation, error) {
	if application == "" || strings.ContainsAny(application, "/?#") {
		return Observation{}, errors.New("Argo CD application name is invalid")
	}
	token, err := client.token(ctx)
	if err != nil {
		return Observation{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/v1/applications/"+url.PathEscape(application), nil)
	if err != nil {
		return Observation{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Observation{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Observation{}, err
	}
	if response.StatusCode == http.StatusNotFound {
		return Observation{State: Pending, Message: "Argo CD application has not been created"}, nil
	}
	if response.StatusCode != http.StatusOK {
		return Observation{}, fmt.Errorf("Argo CD application endpoint returned %d", response.StatusCode)
	}
	var applicationState struct {
		Status struct {
			Sync struct {
				Status   string `json:"status"`
				Revision string `json:"revision"`
			} `json:"sync"`
			Health struct {
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"health"`
			OperationState struct {
				Phase   string `json:"phase"`
				Message string `json:"message"`
			} `json:"operationState"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &applicationState); err != nil {
		return Observation{}, fmt.Errorf("decode Argo CD application: %w", err)
	}
	state := applicationState.Status
	if state.Health.Status == "Degraded" || state.Health.Status == "Missing" || state.OperationState.Phase == "Failed" || state.OperationState.Phase == "Error" {
		message := state.Health.Message
		if message == "" {
			message = state.OperationState.Message
		}
		return Observation{State: Degraded, Revision: state.Sync.Revision, Message: message}, nil
	}
	if state.Sync.Status == "Synced" && state.Health.Status == "Healthy" && (state.OperationState.Phase == "" || state.OperationState.Phase == "Succeeded") {
		return Observation{State: Healthy, Revision: state.Sync.Revision}, nil
	}
	return Observation{State: Pending, Revision: state.Sync.Revision, Message: state.Health.Message}, nil
}
