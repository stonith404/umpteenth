package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/frontend"
	"github.com/stonith404/umpteenth/backend/internal/auth"
	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
)

// registerTestRoutes is set by the e2etest build to mount the test-only endpoints
var registerTestRoutes func(api huma.API, db *database.DB, svc *services)

func initRouter(cfg *config.Config, db *database.DB, svc *services) (huma.API, http.Handler, error) {
	mux := http.NewServeMux()
	api := httpserver.NewAPI(mux, auth.SessionCookieName)
	authn := middleware.NewAuth(svc.auth, svc.apiTokens, auth.SessionCookieName, cfg.App.URL).Required()

	// Mount every module's routes
	svc.auth.RegisterRoutes(api, authn, middleware.RateLimit(svc.loginLimiter, "login", cfg.Server.TrustProxy))
	svc.workspaces.RegisterRoutes(api, authn)
	svc.apiTokens.RegisterRoutes(api, authn)
	svc.settings.RegisterRoutes(api, authn)
	svc.system.RegisterRoutes(api, mux, authn)
	svc.providers.RegisterRoutes(api, authn)
	svc.jobs.RegisterRoutes(api, authn)
	svc.playbook.RegisterRoutes(api, authn)
	svc.images.RegisterRoutes(api, authn)
	svc.secrets.RegisterRoutes(api, authn)
	svc.mcpServers.RegisterRoutes(api, authn)
	svc.skills.RegisterRoutes(api, authn)
	svc.runs.RegisterRoutes(api, authn)
	svc.reflection.RegisterRoutes(api, authn)
	svc.notifications.RegisterRoutes(api, authn)
	svc.stats.RegisterRoutes(api, authn)

	// Test-only endpoints exist only in e2etest builds and never in production
	if registerTestRoutes != nil && !cfg.App.Env.IsProduction() {
		registerTestRoutes(api, db, svc)
	}

	// The SPA catches everything the API did not match
	spa, err := frontend.Handler()
	if errors.Is(err, frontend.ErrFrontendNotIncluded) {
		slog.Warn("Frontend is not included in this build")
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to load frontend: %w", err)
	} else {
		mux.Handle("/", spa)
	}

	handler := httpserver.RequestIDMiddleware(logRequests(securityHeaders(mux)))

	// The MCP endpoint comes last, since its tools are built from the finished spec and served through the full handler like any REST request
	err = svc.mcpAPI.RegisterRoutes(mux, api, handler)
	if err != nil {
		return nil, nil, err
	}

	return api, handler, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps SSE streaming working through the recorder
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs API requests, skipping static assets and health checks to keep the log readable
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/hooks/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelInfo
		}
		slog.Log(r.Context(), level, "HTTP request",
			slog.String("method", r.Method),
			slog.String("path", p),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.String("request_id", httpserver.RequestID(r.Context())),
		)
	})
}
