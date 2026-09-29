//go:build unit

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

func TestRegisterNormalizesHandlerErrors(t *testing.T) {
	// A 5xx cause is only ever logged, so the default logger is captured
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	shared := apperror.NotFound("Widget")
	mux := http.NewServeMux()
	api := NewAPI(mux, "session")
	Register(api, Operation("notFound", http.MethodGet, "/not-found"), nil, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, shared
	})
	Register(api, Operation("providerError", http.MethodGet, "/provider-error"), nil, func(context.Context, *struct{}) (*struct{}, error) {
		return nil, apperror.ProviderError(errors.New("upstream said no"), "The provider failed")
	})
	handler := RequestIDMiddleware(mux)

	get := func(path, requestID string) (int, apperror.Body) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Request-ID", requestID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var body apperror.Body
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return rec.Code, body
	}

	t.Run("client error carries the request ID", func(t *testing.T) {
		status, body := get("/not-found", "req-1")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, apperror.CodeNotFound, body.Code)
		assert.Equal(t, "req-1", body.RequestID)

		// The shared error value is not tagged, so the next request gets its own ID
		_, body = get("/not-found", "req-2")
		assert.Equal(t, "req-2", body.RequestID)
	})

	t.Run("server error logs its cause", func(t *testing.T) {
		status, body := get("/provider-error", "req-3")
		assert.Equal(t, http.StatusBadGateway, status)
		assert.Equal(t, apperror.CodeProviderError, body.Code)
		assert.Equal(t, "req-3", body.RequestID)
		assert.NotContains(t, body.Message, "upstream said no")
		assert.Contains(t, logs.String(), "upstream said no")
	})
}
