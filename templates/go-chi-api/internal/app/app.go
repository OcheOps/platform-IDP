package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

type databasePinger interface {
	Ping(context.Context) error
}

type metrics struct {
	requests atomic.Uint64
}

type contextKey string

const requestIDKey contextKey = "request-id"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func NewHandler(logger *slog.Logger, database databasePinger) http.Handler {
	metricValues := &metrics{}
	router := chi.NewRouter()
	router.Use(requestIDMiddleware)
	router.Use(loggingMiddleware(logger, metricValues))
	router.Get("/health/live", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	router.Get("/health/ready", func(response http.ResponseWriter, request *http.Request) {
		if database != nil {
			ctx, cancel := context.WithTimeout(request.Context(), time.Second)
			defer cancel()
			if err := database.Ping(ctx); err != nil {
				http.Error(response, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		response.WriteHeader(http.StatusNoContent)
	})
	router.Get("/metrics", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintln(response, "# HELP http_requests_total Total HTTP requests handled.")
		fmt.Fprintln(response, "# TYPE http_requests_total counter")
		fmt.Fprintf(response, "http_requests_total %d\n", metricValues.requests.Load())
	})
	return router
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestID := request.Header.Get("X-Request-ID")
		if !validRequestID.MatchString(requestID) {
			var value [16]byte
			if _, err := rand.Read(value[:]); err != nil {
				requestID = fmt.Sprintf("fallback-%d", time.Now().UnixNano())
			} else {
				requestID = hex.EncodeToString(value[:])
			}
		}
		response.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(request.Context(), requestIDKey, requestID)
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func loggingMiddleware(logger *slog.Logger, metricValues *metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			started := time.Now()
			metricValues.requests.Add(1)
			next.ServeHTTP(response, request)
			logger.InfoContext(request.Context(), "request completed",
				"request_id", request.Context().Value(requestIDKey),
				"method", request.Method,
				"path", request.URL.Path,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		})
	}
}
