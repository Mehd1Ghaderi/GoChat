// Package errors carries the errors a caller may see. Only Code and Message
// cross a process boundary; the cause stays in the logs.
package errors

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
)

type Code uint64

// The codes follow gRPC's numbering.
const (
	CodeUnknown            Code = 2
	CodeInvalidArgument    Code = 3
	CodeDeadlineExceeded   Code = 4
	CodeNotFound           Code = 5
	CodeAlreadyExists      Code = 6
	CodePermissionDenied   Code = 7
	CodeResourceExhausted  Code = 8
	CodeFailedPrecondition Code = 9
	CodeUnimplemented      Code = 12
	CodeInternal           Code = 13
	CodeUnavailable        Code = 14
	CodeUnauthenticated    Code = 16
)

func (c Code) String() string {
	switch c {
	case CodeInvalidArgument:
		return "invalid_argument"
	case CodeDeadlineExceeded:
		return "deadline_exceeded"
	case CodeNotFound:
		return "not_found"
	case CodeAlreadyExists:
		return "already_exists"
	case CodePermissionDenied:
		return "permission_denied"
	case CodeResourceExhausted:
		return "resource_exhausted"
	case CodeFailedPrecondition:
		return "failed_precondition"
	case CodeUnimplemented:
		return "unimplemented"
	case CodeInternal:
		return "internal"
	case CodeUnavailable:
		return "unavailable"
	case CodeUnauthenticated:
		return "unauthenticated"
	default:
		return "unknown"
	}
}

type Error struct {
	Code    Code
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return e.Code.String() + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Fields() []zap.Field {
	fields := []zap.Field{zap.String("code", e.Code.String()), zap.String("message", e.Message)}
	if e.Cause != nil {
		fields = append(fields, zap.Error(e.Cause))
	}
	return fields
}

func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func InvalidArgument(message string) *Error { return New(CodeInvalidArgument, message) }

func NotFound(message string) *Error { return New(CodeNotFound, message) }

func AlreadyExists(message string) *Error { return New(CodeAlreadyExists, message) }

func PermissionDenied() *Error { return New(CodePermissionDenied, "permission denied") }

func Unauthenticated() *Error { return New(CodeUnauthenticated, "authentication required") }

func ResourceExhausted(message string) *Error { return New(CodeResourceExhausted, message) }

func FailedPrecondition(message string) *Error { return New(CodeFailedPrecondition, message) }

func Unavailable(message string) *Error { return New(CodeUnavailable, message) }

func Internal(cause error) *Error {
	return &Error{Code: CodeInternal, Message: "internal error", Cause: cause}
}

// As returns err as an *Error, wrapping anything else as internal.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return Internal(err)
}

func CodeOf(err error) Code {
	if err == nil {
		return 0
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	return CodeUnknown
}

func Is(err error, code Code) bool {
	return err != nil && CodeOf(err) == code
}
