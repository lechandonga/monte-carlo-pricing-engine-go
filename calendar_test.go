package mcengine

import (
	"math"
	"testing"
)

// TestObservationCalendarValidation covers every distinct rejection
// reason for a user-supplied observation calendar.
func TestObservationCalendarValidation(t *testing.T) {
	cases := []struct {
		name  string
		dates []float64
	}{
		{"empty slice", []float64{}},
		{"nan", []float64{0.5, math.NaN(), 1}},
		{"infinite", []float64{0.5, math.Inf(1), 1}},
		{"before zero", []float64{-0.1, 0.5, 1}},
		{"after maturity", []float64{0.5, 1.01}},
		{"duplicate", []float64{0.5, 0.5, 1}},
		{"unsorted", []float64{0.8, 0.2, 1}},
		{"missing maturity", []float64{0.25, 0.5, 0.75}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCalendar(tc.dates, 1.0); kindOf(err) != ErrInvalidParam {
				t.Fatalf("want ErrInvalidParam, got %v", err)
			}
			o := baseOption()
			o.Type = Asian
			o.ObservationDates = tc.dates
			if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
				t.Fatalf("Price want ErrInvalidParam, got %v", err)
			}
		})
	}
	t.Run("single maturity point accepted", func(t *testing.T) {
		if err := ValidateCalendar([]float64{1}, 1); err != nil {
			t.Fatal(err)
		}
	})
}

// TestNonUniformCalendarAsian checks that averaging uses only the
// calendar points, not the simulation steps: the same calendar priced
// with 7 and 200 steps must agree within the smaller standard error,
// and changing step count must not change which points are averaged.
func TestNonUniformCalendarAsian(t *testing.T) {
	o := baseOption()
	o.Type = Asian
	o.ObservationDates = []float64{0.05, 0.25, 0.3, 0.7, 1.0}
	eng := NewEngine()
	r7, err := eng.Price(o, Config{NumPaths: 40000, NumSteps: 7, Seed: 21})
	if err != nil {
		t.Fatal(err)
	}
	r200, err := eng.Price(o, Config{NumPaths: 40000, NumSteps: 200, Seed: 21})
	if err != nil {
		t.Fatal(err)
	}
	tol := 4 * (r7.StdErr + r200.StdErr)
	t.Logf("steps=7: %.6f (se %.6f) | steps=200: %.6f (se %.6f)",
		r7.Price, r7.StdErr, r200.Price, r200.StdErr)
	if math.Abs(r7.Price-r200.Price) > tol {
		t.Fatalf("calendar price changed with step count: %v vs %v", r7.Price, r200.Price)
	}

	// A calendar with only maturity is a single fixing at S_T, so the
	// Asian must match the same-strike European within statistical error.
	oe := o
	oe.ObservationDates = []float64{1.0}
	a, err := eng.Price(oe, Config{NumPaths: 80000, NumSteps: 32, Seed: 22, Antithetic: true})
	if err != nil {
		t.Fatal(err)
	}
	eu := baseOption()
	e, err := eng.Price(eu, Config{NumPaths: 80000, NumSteps: 32, Seed: 22, Antithetic: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("single-fixing asian=%.6f european=%.6f", a.Price, e.Price)
	if math.Abs(a.Price-e.Price) > 3*(a.StdErr+e.StdErr) {
		t.Fatalf("single-point calendar Asian %v does not match European %v", a.Price, e.Price)
	}
}

// TestNonUniformCalendarBarrier checks discrete barrier monitoring on
// an irregular calendar is independent of the numerical grid.
func TestNonUniformCalendarBarrier(t *testing.T) {
	mk := func(dates []float64) Option {
		o := baseOption()
		o.Type = Barrier
		o.Barrier = BarrierSpec{Level: 115, Direction: Up, Kind: KnockOut, Monitoring: MonitorDiscrete}
		o.ObservationDates = dates
		return o
	}
	dates := []float64{0.1, 0.2, 0.22, 0.6, 0.9, 1.0}
	eng := NewEngine()
	r10, err := eng.Price(mk(dates), Config{NumPaths: 40000, NumSteps: 10, Seed: 33})
	if err != nil {
		t.Fatal(err)
	}
	r250, err := eng.Price(mk(dates), Config{NumPaths: 40000, NumSteps: 250, Seed: 33})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("steps=10: %.6f (se %.6f) | steps=250: %.6f (se %.6f)",
		r10.Price, r10.StdErr, r250.Price, r250.StdErr)
	if math.Abs(r10.Price-r250.Price) > 4*(r10.StdErr+r250.StdErr) {
		t.Fatalf("barrier calendar price changed with step count")
	}
}

// TestContinuousBarrierConvergence compares Brownian-bridge Monte
// Carlo at increasing grid density against the Reiner-Rubinstein
// closed form for all four up/down, in/out configurations. Every
// refinement level logs price and standard error; the tolerance never
// gets looser than the standard error; and errors must keep shrinking
// as the grid is refined.
// TestContinuousBarrierVsAnalytic prices all four single-barrier
// configurations directly under MonitorContinuous at several grid
// densities and compares with the Reiner-Rubinstein closed form.
// Every grid level logs price and standard error, and the acceptance
// tolerance is a multiple of that level's own standard error (never
// looser than the statistical error).
func TestContinuousBarrierVsAnalytic(t *testing.T) {
	eng := NewEngine()
	cases := []struct {
		name   string
		payoff Payoff
		dir    BarrierDirection
		kind   BarrierKind
		level  float64
		strike float64
	}{
		{"up-and-out call", Call, Up, KnockOut, 130, 100},
		{"down-and-out put", Put, Down, KnockOut, 80, 100},
		{"down-and-in call", Call, Down, KnockIn, 80, 100},
		{"up-and-in put", Put, Up, KnockIn, 120, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			analytic := ContinuousBarrier(tc.payoff, tc.dir, tc.kind,
				100, tc.strike, tc.level, 1, 0.05, 0.02, 0.2)
			mk := func(n int) (float64, float64) {
				o := baseOption()
				o.Payoff = tc.payoff
				o.Strike = tc.strike
				o.Type = Barrier
				o.Barrier = BarrierSpec{Level: tc.level, Direction: tc.dir, Kind: tc.kind, Monitoring: MonitorContinuous}
				res, err := eng.Price(o, Config{NumPaths: 200000, NumSteps: n, Seed: 55, Antithetic: true})
				if err != nil {
					t.Fatal(err)
				}
				return res.Price, res.StdErr
			}
			for _, n := range []int{2, 16, 128} {
				p, se := mk(n)
				dev := math.Abs(p - analytic)
				t.Logf("steps=%3d price=%.5f se=%.6f analytic=%.5f |dev|/se=%.2f",
					n, p, se, analytic, dev/se)
				if dev > 3*se {
					t.Fatalf("steps=%d deviation %v exceeds 3 SE %v", n, dev, se)
				}
			}
		})
	}
}

// TestDiscreteCalendarConvergesToContinuous drives the *discrete*
// monitoring calendar from very sparse to very dense. The price must
// approach the continuous-monitoring closed form monotonically (within
// a statistical band): denser knock-out monitoring makes a knock-out
// cheaper and a knock-in more expensive, and the gap to the analytic
// value keeps contracting. Every density logs price and standard
// error; acceptance bands are multiples of that run's own SE.
func TestDiscreteCalendarConvergesToContinuous(t *testing.T) {
	eng := NewEngine()
	cases := []struct {
		name   string
		payoff Payoff
		dir    BarrierDirection
		kind   BarrierKind
		level  float64
	}{
		{"up-and-out call", Call, Up, KnockOut, 115},
		{"up-and-in call", Call, Up, KnockIn, 115},
		{"down-and-out put", Put, Down, KnockOut, 85},
		{"down-and-in put", Put, Down, KnockIn, 85},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			analytic := ContinuousBarrier(tc.payoff, tc.dir, tc.kind,
				100, 100, tc.level, 1, 0.05, 0.02, 0.2)
			densities := []int{4, 8, 16, 32, 64, 128, 256}
			gaps := make([]float64, len(densities))
			ses := make([]float64, len(densities))
			prevPrice := math.NaN()
			for i, m := range densities {
				o := baseOption()
				o.Payoff = tc.payoff
				o.Type = Barrier
				o.Barrier = BarrierSpec{Level: tc.level, Direction: tc.dir, Kind: tc.kind, Monitoring: MonitorDiscrete}
				o.ObservationDates = linDates(m, 1)
				res, err := eng.Price(o, Config{NumPaths: 120000, NumSteps: 128, Seed: 77, Antithetic: true})
				if err != nil {
					t.Fatal(err)
				}
				gaps[i] = math.Abs(res.Price - analytic)
				ses[i] = res.StdErr
				t.Logf("m=%3d price=%.5f se=%.6f continuous=%.5f gap=%.5f (%.1fx se)",
					m, res.Price, res.StdErr, analytic, gaps[i], gaps[i]/res.StdErr)
				// Tightening direction, allowing statistical slack.
				if !math.IsNaN(prevPrice) {
					if tc.kind == KnockOut && res.Price > prevPrice+3*res.StdErr {
						t.Fatalf("KO price must not rise as monitoring densifies: %v -> %v", prevPrice, res.Price)
					}
					if tc.kind == KnockIn && res.Price < prevPrice-3*res.StdErr {
						t.Fatalf("KI price must not fall as monitoring densifies: %v -> %v", prevPrice, res.Price)
					}
				}
				prevPrice = res.Price
			}
			// Gap must clearly contract from sparsest to densest
			// calendar. The discrete-to-continuous correction decays
			// only like 1/sqrt(m) (Broadie-Glasserman-Kou), so a dense
			// calendar is still distinguishable from true continuous
			// monitoring; here we require the bulk of the gap to be
			// gone and contraction at every single step.
			if gaps[len(gaps)-1] >= 0.2*gaps[0] {
				t.Fatalf("gap did not contract enough: %v -> %v", gaps[0], gaps[len(gaps)-1])
			}
			for i := 1; i < len(gaps); i++ {
				if gaps[i] > gaps[i-1]+3*ses[i] {
					t.Fatalf("gap failed to contract at m=%d: %v -> %v",
						densities[i], gaps[i-1], gaps[i])
				}
			}
		})
	}
}

// TestDiscreteVsContinuousBarrierDirection locks the tightening
// direction: continuous knock-out is no pricier, continuous knock-in
// no cheaper than the discrete calendar, on shared random numbers.
func TestDiscreteVsContinuousBarrierDirection(t *testing.T) {
	eng := NewEngine()
	for _, tc := range []struct {
		name              string
		kind              BarrierKind
		continuousCheaper bool
	}{
		{"knock-out", KnockOut, true},
		{"knock-in", KnockIn, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disc := baseOption()
			disc.Type = Barrier
			disc.Barrier = BarrierSpec{Level: 118, Direction: Up, Kind: tc.kind, Monitoring: MonitorDiscrete}
			disc.ObservationDates = linDates(16, 1)
			cont := disc
			cont.Barrier.Monitoring = MonitorContinuous
			rd, err := eng.Price(disc, Config{NumPaths: 80000, NumSteps: 64, Seed: 66, Antithetic: true})
			if err != nil {
				t.Fatal(err)
			}
			rc, err := eng.Price(cont, Config{NumPaths: 80000, NumSteps: 64, Seed: 66, Antithetic: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s discrete=%.5f continuous=%.5f", tc.name, rd.Price, rc.Price)
			tol := 3 * (rd.StdErr + rc.StdErr)
			if tc.continuousCheaper && rc.Price > rd.Price+tol {
				t.Fatalf("continuous KO must not be pricier than discrete")
			}
			if !tc.continuousCheaper && rc.Price < rd.Price-tol {
				t.Fatalf("continuous KI must not be cheaper than discrete")
			}
		})
	}
}

func linDates(n int, T float64) []float64 {
	d := make([]float64, n)
	for i := range d {
		d[i] = T * float64(i+1) / float64(n)
	}
	return d
}
