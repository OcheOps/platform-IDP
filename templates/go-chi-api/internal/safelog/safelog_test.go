package safelog

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONLoggerRedactsDefaultAndConfiguredFields(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output, "customer_email")
	logger.Info("example", "password", "never-print", "customer_email", "private@example.com", "safe", "visible")
	logLine := output.String()
	for _, forbidden := range []string{"never-print", "private@example.com"} {
		if strings.Contains(logLine, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logLine)
		}
	}
	if !strings.Contains(logLine, `"safe":"visible"`) || strings.Count(logLine, "[REDACTED]") != 2 {
		t.Fatalf("unexpected redacted log: %s", logLine)
	}
}
