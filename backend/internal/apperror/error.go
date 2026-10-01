// Package apperror defines the application errors every API response carries, with a stable code and a client-safe message
package apperror

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type Code string

// #nosec G101 -- these are client-visible error codes, not credentials
const (
	CodeInternal           Code = "internal_error"
	CodeValidationFailed   Code = "validation_failed"
	CodeInvalidRequestBody Code = "invalid_request_body"
	CodeNotFound           Code = "not_found"
	CodeAlreadyInUse       Code = "already_in_use"
	CodeForbidden          Code = "forbidden"
	CodeNotSignedIn        Code = "not_signed_in"
	CodeInvalidToken       Code = "invalid_token"
	CodeRateLimited        Code = "rate_limited"
	CodeConflict           Code = "conflict"
	CodeWorkspaceChanged   Code = "workspace_changed"
	CodeLoginNotConfigured Code = "login_not_configured"
	CodeLoginFailed        Code = "login_failed"
	CodePasskeyFailed      Code = "passkey_failed"
	CodeAccountDisabled    Code = "account_disabled"
	CodeUnsupported        Code = "unsupported"
	CodeProviderError      Code = "provider_error"
	CodeUnavailable        Code = "unavailable"
)

// FieldError describes one safe, client-actionable validation failure
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error is an application error with a stable code, an HTTP status, and an optional internal cause
type Error struct {
	code       Code
	status     int
	message    string
	details    map[string]string
	fields     []FieldError
	retryAfter time.Duration
	cause      error
	requestID  string
}

// New creates a client-safe application error
func New(code Code, status int, message string) *Error {
	return &Error{code: code, status: status, message: message}
}

// wrap creates a client-safe application error that retains an internal cause
func wrap(cause error, code Code, status int, message string) *Error {
	return &Error{code: code, status: status, message: message, cause: cause}
}

// Internal creates a generic server error that retains a diagnostic cause without exposing it to clients
func Internal(cause error) *Error {
	return wrap(cause, CodeInternal, http.StatusInternalServerError, "Something went wrong")
}

// Validation creates a structured validation error
func Validation(fields []FieldError) *Error {
	return New(CodeValidationFailed, http.StatusBadRequest, "Request validation failed").WithFields(fields)
}

// Error returns diagnostic text including the internal cause when one exists
func (e *Error) Error() string {
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

// Unwrap exposes the internal cause to errors.Is and errors.As
func (e *Error) Unwrap() error { return e.cause }

// Is matches application errors by stable code while ignoring message and detail differences
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e.code == t.code
}

// IsCode reports whether an error or one of its wrapped causes has the given application code
func IsCode(err error, code Code) bool {
	return errors.Is(err, &Error{code: code})
}

// As returns the application error inside err, if any
func As(err error) (*Error, bool) {
	return errors.AsType[*Error](err)
}

func (e *Error) Code() Code { return e.code }

// GetStatus returns the HTTP status and makes Error satisfy huma.StatusError
func (e *Error) GetStatus() int { return e.status }

// GetHeaders lets Huma send Retry-After for errors returned from handlers, and the middleware writers reuse it
func (e *Error) GetHeaders() http.Header {
	if e.retryAfter <= 0 {
		return nil
	}
	return http.Header{"Retry-After": []string{strconv.Itoa(int(e.retryAfter.Seconds()) + 1)}}
}

// WithFields attaches structured validation details without exposing the cause
func (e *Error) WithFields(fields []FieldError) *Error {
	e.fields = append([]FieldError(nil), fields...)
	return e
}

// WithDetail attaches one client-safe string detail
func (e *Error) WithDetail(key, value string) *Error {
	if e.details == nil {
		e.details = make(map[string]string)
	}
	e.details[key] = value
	return e
}

// WithRetryAfter attaches a retry delay to the error
func (e *Error) WithRetryAfter(retryAfter time.Duration) *Error {
	e.retryAfter = retryAfter
	return e
}

// WithRequestID returns a copy tagged with the request ID so clients can report it
// It copies because a package-level error value is returned to many requests at once
func (e *Error) WithRequestID(id string) *Error {
	tagged := *e
	tagged.requestID = id
	return &tagged
}

// Body is the JSON shape of every error response
type Body struct {
	Code      Code              `json:"code"`
	Message   string            `json:"message"`
	Fields    []FieldError      `json:"fields,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

// MarshalJSON renders only the client-safe parts of the error
func (e *Error) MarshalJSON() ([]byte, error) {
	return json.Marshal(Body{
		Code:      e.code,
		Message:   e.message,
		Fields:    e.fields,
		Details:   e.details,
		RequestID: e.requestID,
	})
}
