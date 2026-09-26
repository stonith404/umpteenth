package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/common"
)

type requestIDKey struct{}

func init() {
	// Every error leaves the API in the apperror shape, whether it came from a handler, validation, or Huma itself
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return toAppError(context.Background(), status, msg, errs...)
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		reqCtx := context.Background()
		if ctx != nil {
			reqCtx = ctx.Context()
		}
		return toAppError(reqCtx, status, msg, errs...).WithRequestID(RequestID(reqCtx))
	}
}

// NewAPI creates the Huma API mounted on the given mux
func NewAPI(mux *http.ServeMux) huma.API {
	cfg := huma.DefaultConfig("Umpteenth API", common.Version)
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	cfg.SchemasPath = "/api/schemas"
	// Drop the $schema link Huma adds to every response body, so generated TS types stay clean
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: "umpteenth_session"},
		"bearer":  {Type: "http", Scheme: "bearer"},
	}
	api := humago.New(mux, cfg)

	// Errors marshal as apperror.Body, so the spec documents that shape instead of the opaque error type
	api.OpenAPI().Components.Schemas.RegisterTypeAlias(reflect.TypeFor[apperror.Error](), reflect.TypeFor[apperror.Body]())
	return api
}

// Operation describes a route with the shared defaults applied
func Operation(id, method, path string, tags ...string) huma.Operation {
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Tags:        tags,
	}
}

// Register adds an operation with middlewares, keeping module route tables compact
func Register[I, O any](api huma.API, op huma.Operation, mws huma.Middlewares, handler func(context.Context, *I) (*O, error)) {
	op.Middlewares = append(op.Middlewares, mws...)
	huma.Register(api, op, handler)
}

// RequestIDMiddleware assigns every request an ID that shows up in logs and error bodies
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			buf := make([]byte, 8)
			_, _ = rand.Read(buf)
			id = hex.EncodeToString(buf)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestID returns the ID of the current request, if any
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WriteError writes an application error from a Huma middleware
func WriteError(ctx huma.Context, err error) {
	appErr, ok := apperror.As(err)
	if !ok {
		slog.ErrorContext(ctx.Context(), "Request failed", slog.Any("error", err))
		appErr = apperror.Internal(err)
	}
	appErr = appErr.WithRequestID(RequestID(ctx.Context()))

	if retry := appErr.RetryAfter(); retry > 0 {
		ctx.SetHeader("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	}
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(appErr.GetStatus())
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(appErr)
}

// WriteHTTPError writes an application error from a plain net/http handler
func WriteHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	appErr, ok := apperror.As(err)
	if !ok {
		slog.ErrorContext(r.Context(), "Request failed", slog.Any("error", err))
		appErr = apperror.Internal(err)
	}
	appErr = appErr.WithRequestID(RequestID(r.Context()))
	if retry := appErr.RetryAfter(); retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(appErr.GetStatus())
	_ = json.NewEncoder(w).Encode(appErr)
}

func toAppError(ctx context.Context, status int, msg string, errs ...error) *apperror.Error {
	// Handlers return application errors directly, and Huma only wraps them when it adds context
	for _, err := range errs {
		if appErr, ok := apperror.As(err); ok {
			// Server-side failures keep their cause out of the response, so the log is the only place it shows up
			if appErr.GetStatus() >= 500 && appErr.Unwrap() != nil {
				slog.ErrorContext(ctx, "Request failed", slog.String("code", string(appErr.Code())), slog.Any("error", appErr.Unwrap()))
			}
			return appErr
		}
	}

	// Validation problems from Huma's request parsing become structured field errors
	fields := make([]apperror.FieldError, 0, len(errs))
	for _, err := range errs {
		if detail, ok := errors.AsType[*huma.ErrorDetail](err); ok {
			fields = append(fields, apperror.FieldError{Field: detail.Location, Code: "invalid", Message: detail.Message})
		}
	}

	switch {
	case status >= 500:
		slog.ErrorContext(ctx, "Unhandled API error", slog.String("message", msg), slog.Any("errors", errs))
		return apperror.Internal(errors.Join(errs...))
	case status == http.StatusUnprocessableEntity || status == http.StatusBadRequest:
		if len(fields) > 0 {
			return apperror.Validation(fields)
		}
		return apperror.InvalidRequestBody(errors.Join(errs...))
	case status == http.StatusNotFound:
		return apperror.New(apperror.CodeNotFound, status, msg)
	case status == http.StatusUnauthorized:
		return apperror.NotSignedIn()
	case status == http.StatusForbidden:
		return apperror.Forbidden(msg)
	default:
		return apperror.New(apperror.CodeInvalidRequestBody, status, msg)
	}
}
