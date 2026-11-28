// Package inference errors are stable, typed failures. Callers inspect them
// with errors.Is and errors.As. Provider adapters map their native failures to
// these categories; the neutral layer never invents a category.
//
// Retry-After information is preserved where the protocol supplies it, without
// exposing credentials or remote response bodies.
package inference

import (
	"errors"
	"time"
)

var (
	// ErrAuthentication reports missing or invalid credentials.
	ErrAuthentication = errors.New("authentication required")
	// ErrAuthorization reports valid credentials that lack permission.
	ErrAuthorization = errors.New("authorization failed")
	// ErrRateLimit reports that the provider is rate limiting the caller.
	ErrRateLimit = errors.New("rate limited")
	// ErrTimeout reports that an operation exceeded its deadline.
	ErrTimeout = errors.New("operation timed out")
	// ErrCanceled reports that an operation was canceled.
	ErrCanceled = errors.New("operation canceled")
	// ErrUnavailable reports that a provider cannot serve the request.
	ErrUnavailable = errors.New("provider unavailable")
	// ErrUnsupported reports an operation or modality the provider does not support.
	ErrUnsupported = errors.New("operation or modality not supported")
	// ErrInvalidRequest reports a malformed request.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrInvalidResponse reports a malformed provider response.
	ErrInvalidResponse = errors.New("invalid provider response")
	// ErrResourceExhausted reports resource exhaustion (memory, tokens, bytes).
	ErrResourceExhausted = errors.New("resource exhausted")
	// ErrIncompatibleModel reports that the model cannot serve the request.
	ErrIncompatibleModel = errors.New("incompatible model")
	// ErrTransientNative reports a transient native runtime failure.
	ErrTransientNative = errors.New("transient native failure")
)

// RetryAfter carries bounded retry-after information. Zero means unset.
type RetryAfter struct {
	// Delay is the suggested wait before retrying. Zero means unset.
	Delay time.Duration `json:"delay,omitempty"`
	// Source describes where the delay came from (for example "header").
	// It must not contain credentials.
	Source string `json:"source,omitempty"`
}

// IsZero reports whether the retry-after is unset.
func (r RetryAfter) IsZero() bool { return r.Delay == 0 && r.Source == "" }

// RemoteError is a structured error returned by a provider. It carries a stable
// code (a string category), a bounded message, and optional retry-after
// information. Code maps to one of the Err* sentinel errors via Category.
type RemoteError struct {
	// Code is the error category as a string (for example "authentication",
	// "rateLimit", "unavailable"). Use Category to obtain the sentinel error.
	Code string `json:"code"`
	// Message is a human-readable summary, bounded and free of credentials.
	Message string `json:"message,omitempty"`
	// RetryAfter is the suggested wait before retrying.
	RetryAfter RetryAfter `json:"retryAfter,omitempty"`
	// Provider is the provider that originated the error.
	Provider string `json:"provider,omitempty"`
}

func (e *RemoteError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return "remote error"
}

// Unwrap supports errors.Is by mapping the string code to a sentinel error.
func (e *RemoteError) Unwrap() error { return Category(e.Code) }

// Is reports whether the remote error matches a category sentinel.
func (e *RemoteError) Is(target error) bool {
	return errors.Is(Category(e.Code), target)
}

// Category maps a string error code to its sentinel error. Unknown codes
// return ErrInvalidResponse.
func Category(code string) error {
	switch code {
	case "authentication":
		return ErrAuthentication
	case "authorization":
		return ErrAuthorization
	case "rateLimit":
		return ErrRateLimit
	case "timeout":
		return ErrTimeout
	case "canceled":
		return ErrCanceled
	case "unavailable":
		return ErrUnavailable
	case "unsupported":
		return ErrUnsupported
	case "invalidRequest":
		return ErrInvalidRequest
	case "invalidResponse":
		return ErrInvalidResponse
	case "resourceExhausted":
		return ErrResourceExhausted
	case "incompatibleModel":
		return ErrIncompatibleModel
	case "transientNative":
		return ErrTransientNative
	default:
		return ErrInvalidResponse
	}
}

// NewRemoteError builds a RemoteError from a category sentinel. Message is
// truncated to 4096 bytes on a character boundary.
func NewRemoteError(code error, message string, provider string) *RemoteError {
	return &RemoteError{Code: codeName(code), Message: truncate(message), Provider: provider}
}

// codeName returns the string code for a sentinel error.
func codeName(code error) string {
	switch code {
	case ErrAuthentication:
		return "authentication"
	case ErrAuthorization:
		return "authorization"
	case ErrRateLimit:
		return "rateLimit"
	case ErrTimeout:
		return "timeout"
	case ErrCanceled:
		return "canceled"
	case ErrUnavailable:
		return "unavailable"
	case ErrUnsupported:
		return "unsupported"
	case ErrInvalidRequest:
		return "invalidRequest"
	case ErrInvalidResponse:
		return "invalidResponse"
	case ErrResourceExhausted:
		return "resourceExhausted"
	case ErrIncompatibleModel:
		return "incompatibleModel"
	case ErrTransientNative:
		return "transientNative"
	default:
		return "invalidResponse"
	}
}

// truncate bounds an error message to 4096 bytes on a character boundary.
func truncate(value string) string {
	if len(value) <= 4096 {
		return value
	}
	runes := []rune(value)
	if len(runes) > 4096 {
		runes = runes[:4096]
	}
	return string(runes)
}

var _ error = (*RemoteError)(nil)
