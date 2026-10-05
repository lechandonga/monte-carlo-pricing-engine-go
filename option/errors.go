package option

import "fmt"

// ErrorCode is a stable, distinguishable classification of every failure the
// pricing engine can produce. Callers should switch on the code rather than
// match human-readable text.
type ErrorCode string

const (
	// ErrInvalidParams covers structural input problems (unknown enums,
	// non-positive levels, etc.).
	ErrInvalidParams ErrorCode = "INVALID_PARAMS"
	// ErrNegativeVol is reported when volatility is below zero.
	ErrNegativeVol ErrorCode = "NEGATIVE_VOLATILITY"
	// ErrNonPositiveMaturity is reported when T <= 0 or is not finite.
	ErrNonPositiveMaturity ErrorCode = "NON_POSITIVE_MATURITY"
	// ErrInvalidSteps is reported when the step count is not positive.
	ErrInvalidSteps ErrorCode = "INVALID_STEPS"
	// ErrBarrierConfig is reported when barrier fields are inconsistent with
	// the option's direction, spot or strike.
	ErrBarrierConfig ErrorCode = "INVALID_BARRIER_CONFIG"
	// ErrInvalidSimConfig is reported when the simulation configuration is
	// unusable (too few paths, bad worker count, bad tolerance).
	ErrInvalidSimConfig ErrorCode = "INVALID_SIM_CONFIG"
	// ErrTooFewPaths is reported when the usable effective sample count is
	// below the configured minimum (for example, every path produced a
	// non-finite payoff).
	ErrTooFewPaths ErrorCode = "TOO_FEW_PATHS"
	// ErrNotConverged is reported when the Monte Carlo standard error fails
	// the configured convergence tolerance.
	ErrNotConverged ErrorCode = "NOT_CONVERGED"
	// ErrNonFiniteResult is reported when price or standard error is NaN or
	// +/-Inf.
	ErrNonFiniteResult ErrorCode = "NON_FINITE_RESULT"
)

// ValidationError carries a machine-readable code plus the specific reason.
type ValidationError struct {
	Code   ErrorCode
	Reason string
}

func (e *ValidationError) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func fail(code ErrorCode, format string, args ...any) error {
	return &ValidationError{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// ErrorCodeOf extracts the engine error code from an error produced by this
// library, returning an empty string for unrelated errors.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var ve *ValidationError
	if asValidation(err, &ve) {
		return ve.Code
	}
	return ""
}

func asValidation(err error, target **ValidationError) bool {
	for err != nil {
		if ve, ok := err.(*ValidationError); ok {
			*target = ve
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
