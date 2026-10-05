// Package mcengine provides a fully local Monte Carlo option pricing
// library. It uses no external market data or services: all pricing
// parameters are supplied by the caller and all randomness is generated
// locally from explicit seeds.
package mcengine

import "fmt"

// ErrKind classifies failures so callers can distinguish invalid input
// from numerical or resource problems programmatically.
type ErrKind int

const (
	// ErrInvalidParam indicates a parameter failed validation
	// (e.g. negative volatility, non-positive maturity, or a
	// contradictory barrier/strike combination).
	ErrInvalidParam ErrKind = iota
	// ErrTooFewPaths indicates the configured path count is below the
	// minimum required for a statistically meaningful estimate.
	ErrTooFewPaths
	// ErrNotConverged indicates the standard error of the estimate
	// exceeds the configured relative tolerance.
	ErrNotConverged
	// ErrNonFinite indicates a non-finite value (NaN/Inf) appeared
	// during simulation, so no number is returned.
	ErrNonFinite
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrTooFewPaths:
		return "too few paths"
	case ErrNotConverged:
		return "not converged"
	case ErrNonFinite:
		return "non-finite result"
	default:
		return "unknown error"
	}
}

// Error is the single error type returned by this package. Kind is
// always set and allows programmatic handling.
type Error struct {
	Kind   ErrKind
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mcengine: %s: %s", e.Kind, e.Detail)
}

func invalidf(format string, args ...any) *Error {
	return &Error{Kind: ErrInvalidParam, Detail: fmt.Sprintf(format, args...)}
}
