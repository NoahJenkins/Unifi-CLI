package apperr

import "fmt"

type Code string

const (
	AuthFailed            Code = "auth_failed"
	ControllerUnreachable Code = "controller_unreachable"
	NotFound              Code = "not_found"
	AmbiguousID           Code = "ambiguous_id"
	Conflict              Code = "conflict"
	PermissionDenied      Code = "permission_denied"
	ValidationFailed      Code = "validation_failed"
	SafeModeBlocked       Code = "safe_mode_blocked"
	NotImplemented        Code = "not_implemented"
	Internal              Code = "internal"
)

type Error struct {
	Code    Code
	Message string
	Hint    string
}

func (e *Error) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func WithHint(err *Error, hint string) *Error {
	err.Hint = hint
	return err
}

func Is(err error, code Code) bool {
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

func As(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return nil
}
