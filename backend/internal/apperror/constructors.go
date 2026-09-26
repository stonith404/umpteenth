package apperror

import (
	"fmt"
	"net/http"
	"time"
)

// The constructors in this file keep public messages, statuses, and details in one place

func NotFound(resource string) *Error {
	return New(CodeNotFound, http.StatusNotFound, resource+" not found").WithDetail("resource", resource)
}

func AlreadyInUse(property string) *Error {
	return New(CodeAlreadyInUse, http.StatusConflict, property+" is already in use").WithDetail("property", property)
}

func InvalidRequestBody(cause error) *Error {
	return wrap(cause, CodeInvalidRequestBody, http.StatusBadRequest, "Request body is invalid")
}

func InvalidField(field, code, message string) *Error {
	return New(CodeValidationFailed, http.StatusBadRequest, fmt.Sprintf("%s %s", field, message)).WithFields([]FieldError{{
		Field:   field,
		Code:    code,
		Message: message,
	}})
}

func NotSignedIn() *Error {
	return New(CodeNotSignedIn, http.StatusUnauthorized, "You are not signed in")
}

func InvalidToken() *Error {
	return New(CodeInvalidToken, http.StatusUnauthorized, "Token is invalid or expired")
}

func Forbidden(message string) *Error {
	return New(CodeForbidden, http.StatusForbidden, message)
}

func Conflict(message string) *Error {
	return New(CodeConflict, http.StatusConflict, message)
}

func WorkspaceChanged() *Error {
	return New(CodeWorkspaceChanged, http.StatusConflict, "Your session moved to another workspace in another tab, reload the page to continue")
}

func RateLimited(retryAfter time.Duration) *Error {
	return New(CodeRateLimited, http.StatusTooManyRequests, "Too many requests").WithRetryAfter(retryAfter)
}

func LoginNotConfigured() *Error {
	return New(CodeLoginNotConfigured, http.StatusServiceUnavailable, "Sign-in is not configured")
}

func LoginFailed(cause error) *Error {
	return wrap(cause, CodeLoginFailed, http.StatusUnauthorized, "Login failed")
}

func AccountDisabled() *Error {
	return New(CodeAccountDisabled, http.StatusForbidden, "Your account has been deactivated")
}

func Unsupported(message string) *Error {
	return New(CodeUnsupported, http.StatusUnprocessableEntity, message)
}

func ProviderError(cause error, message string) *Error {
	return wrap(cause, CodeProviderError, http.StatusBadGateway, message)
}

func Unavailable(cause error, message string) *Error {
	return wrap(cause, CodeUnavailable, http.StatusServiceUnavailable, message)
}
