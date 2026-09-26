package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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

// routes bundles what modules need to mount their operations
type routes struct {
	api  huma.API
	auth huma.Middlewares
}

func initRouter(cfg *config.Config, db *database.DB, svc *services) (huma.API, http.Handler, error) {
	mux := http.NewServeMux()
	api := httpserver.NewAPI(mux)

	authMiddleware := middleware.NewAuth(svc.auth, svc.apiTokens, auth.SessionCookieName, cfg.App.URL)
	r := routes{api: api, auth: authMiddleware.Required()}

	// Mount every module's routes
	svc.auth.RegisterRoutes(api, r.auth, middleware.RateLimit(svc.loginLimiter, "login", cfg.Server.TrustProxy))
	svc.apiTokens.RegisterRoutes(api, r.auth)
	svc.settings.RegisterRoutes(api, r.auth)
	svc.system.RegisterRoutes(api, mux, r.auth)
	registerModuleRoutes(r, svc)

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

	return api, httpserver.RequestIDMiddleware(logRequests(securityHeaders(mux))), nil
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
		if len(p) < 5 || (p[:5] != "/api/" && p[:min(len(p), 7)] != "/hooks/") {
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

// registerModuleRoutes mounts the routes of the job and run modules
func registerModuleRoutes(r routes, svc *services) {
	svc.providers.RegisterRoutes(r.api, r.auth)
	svc.jobs.RegisterRoutes(r.api, r.auth, nil)
	svc.playbook.RegisterRoutes(r.api, r.auth)
	svc.images.RegisterRoutes(r.api, r.auth)
	svc.secrets.RegisterRoutes(r.api, r.auth)
	svc.mcpServers.RegisterRoutes(r.api, r.auth)
	svc.runs.RegisterRoutes(r.api, r.auth)
	svc.reflection.RegisterRoutes(r.api, r.auth)
	svc.notifications.RegisterRoutes(r.api, r.auth)
	svc.stats.RegisterRoutes(r.api, r.auth)
}
