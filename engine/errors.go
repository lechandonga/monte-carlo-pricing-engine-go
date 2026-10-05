package engine

import (
	"fmt"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

func mkErr(code option.ErrorCode, format string, args ...any) error {
	return &option.ValidationError{Code: code, Reason: fmt.Sprintf(format, args...)}
}
