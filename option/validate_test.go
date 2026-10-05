package option_test

import (
	"errors"
	"math"
	"testing"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

func baseParams() option.Params {
	return option.Params{
		Kind: option.European, Right: option.Call,
		Spot: 100, Strike: 100, T: 1,
		R: 0.05, Q: 0.0, Vol: 0.2, Steps: 12,
	}
}

func baseBarrier() option.Params {
	p := baseParams()
	p.Kind = option.Barrier
	p.Direction = option.Up
	p.BarType = option.KnockOut
	p.Barrier = 120
	p.Monitoring = option.Continuous
	p.Steps = 50
	return p
}

// TestInvalidParams enumerates the business-level rejection cases. Every
// case must fail with its own distinguishable ErrorCode; the engine must
// never repair an input or return a default-looking number.
func TestInvalidParams(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*option.Params)
		code option.ErrorCode
	}{
		{"unknown kind", func(p *option.Params) { p.Kind = 0 }, option.ErrInvalidParams},
		{"unknown right", func(p *option.Params) { p.Right = 9 }, option.ErrInvalidParams},
		{"zero spot", func(p *option.Params) { p.Spot = 0 }, option.ErrInvalidParams},
		{"negative strike", func(p *option.Params) { p.Strike = -1 }, option.ErrInvalidParams},
		{"nan rate", func(p *option.Params) { p.R = nan() }, option.ErrInvalidParams},
		{"negative vol", func(p *option.Params) { p.Vol = -0.01 }, option.ErrNegativeVol},
		{"zero maturity", func(p *option.Params) { p.T = 0 }, option.ErrNonPositiveMaturity},
		{"negative maturity", func(p *option.Params) { p.T = -0.5 }, option.ErrNonPositiveMaturity},
		{"inf maturity", func(p *option.Params) { p.T = inf() }, option.ErrNonPositiveMaturity},
		{"zero steps", func(p *option.Params) { p.Steps = 0 }, option.ErrInvalidSteps},
		{"negative rebate", func(p *option.Params) { p.Rebate = -1 }, option.ErrInvalidParams},

		// Barrier-specific contradictions.
		{"barrier no direction", func(p *option.Params) { p.Direction = 0 }, option.ErrBarrierConfig},
		{"barrier no type", func(p *option.Params) { p.BarType = 0 }, option.ErrBarrierConfig},
		{"barrier no monitoring", func(p *option.Params) { p.Monitoring = 0 }, option.ErrBarrierConfig},
		{"up barrier below spot", func(p *option.Params) { p.Barrier = 90 }, option.ErrBarrierConfig},
		{"up barrier at spot", func(p *option.Params) { p.Barrier = 100 }, option.ErrBarrierConfig},
		{"down barrier above spot", func(p *option.Params) {
			p.Direction = option.Down
			p.Barrier = 110
		}, option.ErrBarrierConfig},
		{"up-and-out call barrier below strike", func(p *option.Params) {
			p.Barrier = 95
			p.Direction = option.Up
		}, option.ErrBarrierConfig},
		{"down-and-out put barrier above strike", func(p *option.Params) {
			p.Direction = option.Down
			p.BarType = option.KnockOut
			p.Right = option.Put
			p.Barrier = 105
			p.Monitoring = option.Discrete
		}, option.ErrBarrierConfig},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseBarrier()
			tc.mut(&p)
			err := p.Validate()
			if err == nil {
				t.Fatalf("%s: expected rejection, params were accepted", tc.name)
			}
			var ve *option.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("%s: error is not *ValidationError: %v", tc.name, err)
			}
			if ve.Code != tc.code {
				t.Fatalf("%s: code = %s (%s), want %s", tc.name, ve.Code, ve.Reason, tc.code)
			}
			t.Logf("business=invalid_params case=%-38s code=%s reason=%q", tc.name, ve.Code, ve.Reason)
		})
	}
}

func TestValidParamsAccepted(t *testing.T) {
	for _, name := range []string{"european", "asian", "barrier-continuous", "barrier-discrete", "zero-vol"} {
		p := baseParams()
		switch name {
		case "asian":
			p.Kind = option.Asian
		case "barrier-continuous":
			p = baseBarrier()
		case "barrier-discrete":
			p = baseBarrier()
			p.Monitoring = option.Discrete
		case "zero-vol":
			p.Vol = 0
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		t.Logf("business=param_acceptance case=%s result=accepted", name)
	}
}

func nan() float64 { return math.NaN() }
func inf() float64 { return math.Inf(1) }
