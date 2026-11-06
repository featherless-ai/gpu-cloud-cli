// Package errs holds the CLI's error kinds. Every message is shown to the
// person as is, so it must make sense without context.
package errs

import (
	"errors"
	"fmt"
)

type Kind int

const (
	KindCLI Kind = iota
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindRetryable
	KindRateLimit
	KindServiceUnavailable
)

type Error struct {
	Kind    Kind
	Message string
	// Code is the API's machine code on a conflict.
	Code string
}

func (e *Error) Error() string { return e.Message }

// Retryable covers rate limits, unavailable services and lost connections.
func (e *Error) Retryable() bool {
	return e.Kind == KindRetryable || e.Kind == KindRateLimit || e.Kind == KindServiceUnavailable
}

func New(message string) error { return &Error{Kind: KindCLI, Message: message} }

func Newf(format string, args ...any) error { return New(fmt.Sprintf(format, args...)) }

func Unauthorized(message string) error {
	return &Error{Kind: KindUnauthorized, Message: orDefault(message, "Unauthorized")}
}

// Forbidden means authenticated, but the key lacks a required scope.
func Forbidden(message string) error {
	return &Error{Kind: KindForbidden, Message: orDefault(message, "Forbidden")}
}

// NotFound means missing or outside the caller's organization.
func NotFound(message string) error {
	return &Error{Kind: KindNotFound, Message: orDefault(message, "Not found")}
}

// Conflict means the server rejected the request state (409).
func Conflict(code, message string) error {
	return &Error{Kind: KindConflict, Message: orDefault(message, "Conflict"), Code: code}
}

func Retryable(message string) error { return &Error{Kind: KindRetryable, Message: message} }

func RateLimit(message string) error {
	return &Error{Kind: KindRateLimit, Message: orDefault(message, "Rate limited")}
}

func ServiceUnavailable(message string) error {
	return &Error{Kind: KindServiceUnavailable, Message: orDefault(message, "Service unavailable")}
}

func orDefault(message, fallback string) string {
	if message == "" {
		return fallback
	}

	return message
}

// As returns the CLI error inside err, if any.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// IsCLI reports whether err is one of the CLI's errors (any kind).
func IsCLI(err error) bool {
	_, ok := As(err)
	return ok
}

func IsKind(err error, kind Kind) bool {
	e, ok := As(err)
	return ok && e.Kind == kind
}

func IsRetryable(err error) bool {
	e, ok := As(err)
	return ok && e.Retryable()
}

func ConflictCode(err error) string {
	if e, ok := As(err); ok && e.Kind == KindConflict {
		return e.Code
	}

	return ""
}

func Describe(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// Exit ends the process with Code. The message, if any, was already shown.
type Exit struct{ Code int }

func (e *Exit) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode returns the code an error should end the process with.
func ExitCode(err error) (int, bool) {
	var exit *Exit

	if errors.As(err, &exit) {
		return exit.Code, true
	}

	return 0, false
}
