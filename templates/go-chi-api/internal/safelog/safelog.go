package safelog

import (
	"io"
	"log/slog"
	"strings"
)

func NewJSON(output io.Writer, configuredFields string) *slog.Logger {
	redacted := map[string]struct{}{
		"authorization": {}, "database_url": {}, "password": {}, "secret": {}, "token": {},
	}
	for _, field := range strings.Split(configuredFields, ",") {
		if field = strings.ToLower(strings.TrimSpace(field)); field != "" {
			redacted[field] = struct{}{}
		}
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if _, found := redacted[strings.ToLower(attribute.Key)]; found {
				return slog.String(attribute.Key, "[REDACTED]")
			}
			return attribute
		},
	})
	return slog.New(handler)
}
