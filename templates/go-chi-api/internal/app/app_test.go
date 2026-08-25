package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDatabase struct{ err error }

func (database fakeDatabase) Ping(context.Context) error { return database.err }

func TestPlatformEndpointsAndStructuredLogs(t *testing.T) {
	var logs bytes.Buffer
	handler := NewHandler(slog.New(slog.NewJSONHandler(&logs, nil)), nil)

	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		request := httptest.NewRequest(http.MethodGet, path+"?token=must-not-be-logged", nil)
		request.Header.Set("Authorization", "must-not-be-logged")
		request.Header.Set("X-Request-ID", "test-request")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code < 200 || response.Code >= 300 {
			t.Fatalf("%s returned %d", path, response.Code)
		}
		if response.Header().Get("X-Request-ID") != "test-request" {
			t.Fatalf("%s did not propagate request ID", path)
		}
	}
	if !strings.Contains(logs.String(), `"request_id":"test-request"`) {
		t.Fatalf("request ID absent from JSON logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), "must-not-be-logged") {
		t.Fatalf("sensitive request data leaked to logs: %s", logs.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON log %q: %v", line, err)
		}
	}
}

func TestReadinessReflectsDatabaseHealth(t *testing.T) {
	handler := NewHandler(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), fakeDatabase{err: errors.New("unavailable")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
