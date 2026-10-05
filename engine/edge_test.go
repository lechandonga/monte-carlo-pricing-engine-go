package engine_test

import (
	"testing"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/engine"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

// TestNonFiniteAndExtremeInputs feeds parameters that make simulated
// exponentials overflow; the engine must report a typed error (too few
// finite samples or non-finite result), never return an Inf price.
func TestNonFiniteAndExtremeInputs(t *testing.T) {
	p := option.Params{Kind: option.European, Right: option.Call,
		Spot: 1e300, Strike: 1, T: 200, R: 0.05, Q: 0, Vol: 2, Steps: 1}
	if err := p.Validate(); err != nil {
		t.Fatalf("setup params should validate (vol 10 legal): %v", err)
	}
	r, err := engine.Price(p, engine.Config{Paths: 20000, Seed: 3, Workers: 2, MinPaths: 1024})
	if err == nil {
		t.Fatalf("expected statistical failure, got result %+v", r)
	}
	code := option.ErrorCodeOf(err)
	if code != option.ErrTooFewPaths && code != option.ErrNonFiniteResult {
		t.Fatalf("unexpected code %s: %v", code, err)
	}
	if r.NonFinite == 0 && code != option.ErrNonFiniteResult {
		t.Fatalf("expected non-finite paths to be counted")
	}
	t.Logf("business=nonfinite case=overflowing_exponential code=%s nonfinite=%d result=rejected",
		code, r.NonFinite)
}
