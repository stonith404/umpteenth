package apperror

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	CodeOIDCNotConfigured  Code = "oidc_not_configured"
	CodeOIDCLoginFailed    Code = "oidc_login_failed"
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

// Wrap creates a client-safe application error that retains an internal cause
func Wrap(cause error, code Code, status int, message string) *Error {
	return &Error{code: code, status: status, message: message, cause: cause}
}

// Internal creates a generic server error that retains a diagnostic cause without exposing it to clients
func Internal(cause error) *Error {
	return Wrap(cause, CodeInternal, http.StatusInternalServerError, "Something went wrong")
}

// Validation creates a structured validation error
func Validation(fields []FieldError) *Error {
	return New(CodeValidationFailed, http.StatusBadRequest, "Request validation failed").WithFields(fields)
}

// Error returns diagnostic text including the internal cause when one exists
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

// Unwrap exposes the internal cause to errors.Is and errors.As
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is matches application errors by stable code while ignoring message and detail differences
func (e *Error) Is(target error) bool {
	targetError, ok := target.(*Error)
	return ok && e != nil && targetError != nil && e.code == targetError.code
}

// IsCode reports whether an error or one of its wrapped causes has the given application code
func IsCode(err error, code Code) bool {
	return errors.Is(err, &Error{code: code})
}

// As returns the application error inside err, if any
func As(err error) (*Error, bool) {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

func (e *Error) Code() Code { return e.code }

// GetStatus returns the HTTP status and makes Error satisfy huma.StatusError
func (e *Error) GetStatus() int {
	if e == nil || e.status == 0 {
		return http.StatusInternalServerError
	}
	return e.status
}

// ClientMessage returns the message that is safe to include in an HTTP response
func (e *Error) ClientMessage() string { return e.message }

// Details returns a copy of the additional client-safe details
func (e *Error) Details() map[string]string {
	if len(e.details) == 0 {
		return nil
	}
	return maps.Clone(e.details)
}

// Fields returns a copy of the structured validation details
func (e *Error) Fields() []FieldError {
	return append([]FieldError(nil), e.fields...)
}

// RetryAfter returns the duration a client should wait before retrying
func (e *Error) RetryAfter() time.Duration { return e.retryAfter }

// GetHeaders lets Huma send Retry-After for errors returned from handlers, not only from middlewares
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

// WithRequestID attaches the request ID so clients can report it
func (e *Error) WithRequestID(id string) *Error {
	e.requestID = id
	return e
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
