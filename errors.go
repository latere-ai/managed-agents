// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package managed

import (
	"errors"
	"fmt"
	"time"
)

// Errors this package returns. Every typed error below unwraps to one of them,
// so a caller matches the class with errors.Is and never on an adapter's own
// error type.
var (
	// ErrInvalidSpec reports a specification that is malformed on its own terms,
	// before any runtime is consulted.
	ErrInvalidSpec = errors.New("invalid managed session specification")
	// ErrUnsupported reports a well-formed specification asking for something
	// this runtime cannot do. It is a refusal made before anything remote is
	// created, never a license to run with the unsupported part dropped.
	ErrUnsupported = errors.New("managed session capability unsupported")
	// ErrRuntimeMismatch reports a reference handed to an adapter that did not
	// start the session. Routing on RuntimeName is what stops one adapter from
	// interpreting another's locator.
	ErrRuntimeMismatch = errors.New("managed session runtime mismatch")
	// ErrNotFound reports a session, or one of its artifacts, that the runtime no
	// longer holds. It is a legitimate answer after a release or an expiry, so
	// a caller ignores it while cancelling, and accepts it as confirmation
	// where it already holds a terminal observation to fall back on.
	ErrNotFound = errors.New("managed session not found")
	// ErrContract reports an adapter breaking a guarantee this package makes
	// about observations — a regressed state, a shrinking usage total, a verdict
	// on an ungraded session. Nothing the runtime reports afterwards can be
	// trusted, so the observation is never absorbed quietly.
	ErrContract = errors.New("managed runtime contract violation")
)

// InvalidSpecError names the specification field that is wrong and says why, so
// a caller can point at the offending input instead of re-validating to find
// it. It unwraps to ErrInvalidSpec.
type InvalidSpecError struct {
	Field   string
	Message string
}

func (e *InvalidSpecError) Error() string {
	return fmt.Sprintf("%v: %s: %s", ErrInvalidSpec, e.Field, e.Message)
}
func (e *InvalidSpecError) Unwrap() error { return ErrInvalidSpec }

// UnsupportedError records which runtime refused which capability, and the
// specification field that asked for it. Capability is a semantic identifier
// such as "network:open" or "package_manager:pip" rather than a vendor product
// name, so the refusal reads the same whichever adapter raised it.
type UnsupportedError struct {
	Runtime    RuntimeName
	Field      string
	Capability string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%v: runtime %q does not support %s (%s)", ErrUnsupported, e.Runtime, e.Capability, e.Field)
}
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

// RuntimeMismatchError reports a reference routed to the wrong adapter. It
// carries both names because the interesting question is which pairing went
// wrong, not merely that one did. It unwraps to ErrRuntimeMismatch.
type RuntimeMismatchError struct {
	Expected RuntimeName
	Actual   RuntimeName
}

func (e *RuntimeMismatchError) Error() string {
	return fmt.Sprintf("%v: expected %q, got %q", ErrRuntimeMismatch, e.Expected, e.Actual)
}
func (e *RuntimeMismatchError) Unwrap() error { return ErrRuntimeMismatch }

// NotFoundError names what the runtime no longer holds. ID is a session id, or
// an artifact id where the lookup was for one file of a session that still
// exists. It unwraps to ErrNotFound.
type NotFoundError struct {
	Runtime RuntimeName
	ID      string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%v: %s/%s", ErrNotFound, e.Runtime, e.ID)
}
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// TemporaryError marks an upstream failure a caller may retry.
type TemporaryError struct {
	Err   error
	After time.Duration
}

func (e *TemporaryError) Error() string { return e.Err.Error() }
func (e *TemporaryError) Unwrap() error { return e.Err }

// Temporary marks the failure as retryable. IsTemporary matches on this method
// structurally rather than on the type, so an adapter may pass a transport's
// own retryable error through without wrapping it.
func (e *TemporaryError) Temporary() bool { return true }

// RetryAfter is how long the upstream asked the caller to wait, typically from
// a rate-limit hint. It is a floor and not a schedule: a hint shorter than the
// caller's current backoff does not shorten it, and a zero After leaves the
// backoff entirely to the caller.
func (e *TemporaryError) RetryAfter() time.Duration {
	return e.After
}

// IsTemporary reports whether anything in err's chain claims to be retryable.
// The test is structural — any error with a Temporary() bool method qualifies —
// so a runtime need not adopt this package's error type to be retried.
func IsTemporary(err error) bool {
	var temporary interface{ Temporary() bool }
	return errors.As(err, &temporary) && temporary.Temporary()
}

// CleanupError wraps a failure that happened while tearing a session down,
// after the run itself had already finished or failed. It is joined to the
// original cause rather than replacing it, so a cleanup problem never hides why
// the session ended.
type CleanupError struct{ Err error }

func (e *CleanupError) Error() string { return "managed session cleanup: " + e.Err.Error() }
func (e *CleanupError) Unwrap() error { return e.Err }
