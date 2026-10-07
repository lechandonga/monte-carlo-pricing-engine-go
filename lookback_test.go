package mcengine

import (
	"math"
	"testing"
)

func lookbackOption(p Payoff, kind LookbackKind, mon Monitoring, dates []float64) Option {
	o := baseOption()
	o.Type = Lookback
	o.Payoff = p
	o.Lookback = LookbackSpec{Kind: kind, Monitoring: mon}
	o.ObservationDates = dates
	return o
}

// TestLookbackValidation mirrors the calendar/parameter rejection
// rules for the new product.
func TestLookbackValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		mon  Monitoring
		kind LookbackKind
		ok   bool
	}{
		{"discrete floating", MonitorDiscrete, FloatingStrike, true},
		{"continuous fixed", MonitorContinuous, FixedStrike, true},
		{"bad monitoring", Monitoring(9), FloatingStrike, false},
		{"bad kind", MonitorDiscrete, LookbackKind(7), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := lookbackOption(Call, tc.kind, tc.mon, linDates(16, 1))
			err := o.Validate()
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && kindOf(err) != ErrInvalidParam {
				t.Fatalf("want ErrInvalidParam, got %v", err)
			}
		})
	}
	t.Run("bad calendar rejected", func(t *testing.T) {
		o := lookbackOption(Call, FloatingStrike, MonitorDiscrete,
			[]float64{0.5, 0.2, 1})
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("want ErrInvalidParam, got %v", err)
		}
	})
}

// TestContinuousFloatingLookbackVsAnalytic checks the continuously
// monitored floating-strike lookback Monte Carlo against the
// Goldman-Sosin-Gatto closed form for call and put.
func TestContinuousFloatingLookbackVsAnalytic(t *testing.T) {
	eng := NewEngine()
	for _, p := range []Payoff{Call, Put} {
		t.Run(p.String(), func(t *testing.T) {
			o := lookbackOption(p, FloatingStrike, MonitorContinuous, nil)
			res, err := eng.Price(o, Config{NumPaths: 300000, NumSteps: 128, Seed: 8, Antithetic: true})
			if err != nil {
				t.Fatal(err)
			}
			an := ContinuousFloatingLookback(p, 100, 1, 0.05, 0.02, 0.2)
			dev := math.Abs(res.Price - an)
			t.Logf("mc=%.5f se=%.6f analytic=%.5f |dev|/se=%.2f",
				res.Price, res.StdErr, an, dev/res.StdErr)
			if dev > 3*res.StdErr {
				t.Fatalf("lookback %v deviation %v beyond 3 SE %v", p, dev, res.StdErr)
			}
		})
	}
}

// TestLookbackDiscreteConvergesToContinuous drives the discrete
// observation calendar from sparse to dense and requires the floating
// lookback price to approach the continuous result monotonically
// (continuous monitoring can only see a larger extreme).
func TestLookbackDiscreteConvergesToContinuous(t *testing.T) {
	eng := NewEngine()
	for _, p := range []Payoff{Call, Put} {
		t.Run(p.String(), func(t *testing.T) {
			analytic := ContinuousFloatingLookback(p, 100, 1, 0.05, 0.02, 0.2)
			ms := []int{4, 8, 16, 32, 64, 128}
			prev := math.NaN()
			var firstGap, lastGap float64
			var lastSE float64
			for i, nObs := range ms {
				o := lookbackOption(p, FloatingStrike, MonitorDiscrete, linDates(nObs, 1))
				res, err := eng.Price(o, Config{NumPaths: 150000, NumSteps: 256, Seed: 9, Antithetic: true})
				if err != nil {
					t.Fatal(err)
				}
				gap := analytic - res.Price // continuous >= discrete
				t.Logf("m=%3d price=%.5f se=%.6f continuous=%.5f gap=%.5f (%.1fx se)",
					nObs, res.Price, res.StdErr, analytic, gap, gap/res.StdErr)
				if !math.IsNaN(prev) && res.Price < prev-3*res.StdErr {
					t.Fatalf("discrete lookback price must rise with denser calendar: %v -> %v", prev, res.Price)
				}
				if gap < -3*res.StdErr {
					t.Fatalf("discrete price %v exceeds continuous %v beyond noise", res.Price, analytic)
				}
				prev = res.Price
				if i == 0 {
					firstGap = gap
				}
				lastGap, lastSE = gap, res.StdErr
			}
			if lastGap <= 0 || lastGap > firstGap {
				t.Fatalf("gap to continuous did not contract: %v -> %v", firstGap, lastGap)
			}
			t.Logf("gap contraction %.5f -> %.5f (last %.1fx se)", firstGap, lastGap, lastGap/lastSE)
		})
	}
}

// TestLookbackHardRelations enforces:
//   - fixed-strike lookback >= same-strike same-direction European
//   - floating-strike lookback >= European struck at spot
//   - continuous monitoring >= discrete monitoring
//   - antithetic switch: clearly smaller SE, unchanged price
func TestLookbackHardRelations(t *testing.T) {
	eng := NewEngine()
	for _, p := range []Payoff{Call, Put} {
		t.Run(p.String(), func(t *testing.T) {
			price := func(o Option) Result {
				r, err := eng.Price(o, Config{NumPaths: 200000, NumSteps: 128, Seed: 13, Antithetic: true})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}

			fixed := price(lookbackOption(p, FixedStrike, MonitorContinuous, nil))
			vanilla := baseOption()
			vanilla.Payoff = p
			ev := price(vanilla)
			t.Logf("fixed lookback=%.4f european=%.4f", fixed.Price, ev.Price)
			if fixed.Price < ev.Price-3*(fixed.StdErr+ev.StdErr) {
				t.Fatalf("fixed-strike lookback %v below European %v", fixed.Price, ev.Price)
			}

			floating := price(lookbackOption(p, FloatingStrike, MonitorContinuous, nil))
			atm := baseOption()
			atm.Payoff = p
			atm.Strike = 100
			atmV := price(atm)
			t.Logf("floating lookback=%.4f ATM european=%.4f", floating.Price, atmV.Price)
			if floating.Price < atmV.Price-3*(floating.StdErr+atmV.StdErr) {
				t.Fatalf("floating-strike lookback %v below spot-strike European %v",
					floating.Price, atmV.Price)
			}

			discrete := price(lookbackOption(p, FloatingStrike, MonitorDiscrete, linDates(16, 1)))
			t.Logf("continuous=%.4f discrete=%.4f", floating.Price, discrete.Price)
			if discrete.Price > floating.Price+3*(discrete.StdErr+floating.StdErr) {
				t.Fatalf("continuous lookback %v below discrete %v", floating.Price, discrete.Price)
			}

			plain, err := eng.Price(lookbackOption(p, FloatingStrike, MonitorContinuous, nil),
				Config{NumPaths: 100000, NumSteps: 128, Seed: 13})
			if err != nil {
				t.Fatal(err)
			}
			anti, err := eng.Price(lookbackOption(p, FloatingStrike, MonitorContinuous, nil),
				Config{NumPaths: 100000, NumSteps: 128, Seed: 13, Antithetic: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("plain se=%.5f anti se=%.5f prices %.4f / %.4f",
				plain.StdErr, anti.StdErr, plain.Price, anti.Price)
			if anti.StdErr >= plain.StdErr {
				t.Fatalf("antithetic did not reduce SE: %v vs %v", anti.StdErr, plain.StdErr)
			}
			if math.Abs(anti.Price-plain.Price) > 3*(anti.StdErr+plain.StdErr) {
				t.Fatalf("antithetic price drifted: %v vs %v", anti.Price, plain.Price)
			}
		})
	}
}

// TestLookbackGreeks checks sensitivities are finite, stable across
// repeated calls, and have the expected sign for an ATM floating call.
func TestLookbackGreeks(t *testing.T) {
	o := lookbackOption(Call, FloatingStrike, MonitorContinuous, nil)
	cfg := Config{NumPaths: 60000, NumSteps: 64, Seed: 5, Antithetic: true}
	eng := NewEngine()
	g1, err := eng.Greeks(o, cfg)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := eng.Greeks(o, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g1 != g2 {
		t.Fatalf("repeated greeks differ: %+v vs %+v", g1, g2)
	}
	t.Logf("delta=%.4f gamma=%.6f vega=%.4f theta=%.4f rho=%.4f",
		g1.Delta, g1.Gamma, g1.Vega, g1.Theta, g1.Rho)
	if !(g1.Delta > 0 && g1.Delta < 2) || g1.Gamma < 0 || g1.Vega <= 0 {
		t.Fatalf("unexpected lookback greeks: %+v", g1)
	}
}
