package mcengine

import (
	"errors"
	"math"
	"testing"
)

func baseOption() Option {
	return Option{
		Type: European, Payoff: Call,
		Spot: 100, Strike: 100, Maturity: 1,
		Rate: 0.05, DivYield: 0.02, Vol: 0.2,
	}
}

func baseConfig() Config {
	return Config{NumPaths: 20000, NumSteps: 64, Seed: 42}
}

func kindOf(err error) ErrKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Option)
	}{
		{"negative vol", func(o *Option) { o.Vol = -0.1 }},
		{"zero maturity", func(o *Option) { o.Maturity = 0 }},
		{"negative maturity", func(o *Option) { o.Maturity = -1 }},
		{"zero spot", func(o *Option) { o.Spot = 0 }},
		{"negative strike", func(o *Option) { o.Strike = -5 }},
		{"NaN rate", func(o *Option) { o.Rate = math.NaN() }},
		{"infinite vol", func(o *Option) { o.Vol = math.Inf(1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := baseOption()
			tc.mutate(&o)
			_, err := NewEngine().Price(o, baseConfig())
			if kindOf(err) != ErrInvalidParam {
				t.Fatalf("want ErrInvalidParam, got %v", err)
			}
			t.Logf("rejected as expected: %v", err)
		})
	}
}

func TestBarrierValidation(t *testing.T) {
	mk := func() Option {
		o := baseOption()
		o.Type = Barrier
		o.Barrier = BarrierSpec{Level: 120, Direction: Up, Kind: KnockOut, Monitoring: MonitorDiscrete}
		return o
	}
	t.Run("up barrier below spot", func(t *testing.T) {
		o := mk()
		o.Barrier.Level = 90
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("down barrier above spot", func(t *testing.T) {
		o := mk()
		o.Barrier = BarrierSpec{Level: 110, Direction: Down, Kind: KnockOut, Monitoring: MonitorDiscrete}
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("contradictory up-and-out call", func(t *testing.T) {
		o := mk()
		o.Barrier.Level = 95 // <= strike and <= spot: rejected on side check
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("got %v", err)
		}
		o.Barrier.Level = 100 // <= strike(100)? equal: contradictory
		o.Strike = 110
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("contradictory down-and-out put", func(t *testing.T) {
		o := mk()
		o.Payoff = Put
		o.Strike = 90
		o.Barrier = BarrierSpec{Level: 95, Direction: Down, Kind: KnockOut, Monitoring: MonitorDiscrete}
		if _, err := NewEngine().Price(o, baseConfig()); kindOf(err) != ErrInvalidParam {
			t.Fatalf("got %v", err)
		}
	})
}

func TestEuropeanVsBlackScholes(t *testing.T) {
	for _, p := range []Payoff{Call, Put} {
		t.Run(p.String(), func(t *testing.T) {
			o := baseOption()
			o.Payoff = p
			res, err := NewEngine().Price(o, baseConfig())
			if err != nil {
				t.Fatal(err)
			}
			bs := BlackScholes(p, o.Spot, o.Strike, o.Maturity, o.Rate, o.DivYield, o.Vol)
			tol := 3*res.StdErr + 1e-4
			t.Logf("mc=%.6f se=%.6f bs=%.6f tol=%.6f", res.Price, res.StdErr, bs, tol)
			if math.Abs(res.Price-bs) > tol {
				t.Fatalf("MC price %f deviates from BS %f beyond %f", res.Price, bs, tol)
			}
			if bs < res.CILow-1e-9 || bs > res.CIHigh+1e-9 {
				t.Fatalf("BS %f outside 95%% CI [%f, %f]", bs, res.CILow, res.CIHigh)
			}
		})
	}
}

func TestAsianStepConvergence(t *testing.T) {
	o := baseOption()
	o.Type = Asian
	eng := NewEngine()
	coarse, err := eng.Price(o, Config{NumPaths: 40000, NumSteps: 16, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	fine, err := eng.Price(o, Config{NumPaths: 40000, NumSteps: 256, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	tol := 3 * (coarse.StdErr + fine.StdErr)
	t.Logf("coarse(16)=%.6f fine(256)=%.6f diff=%.6f tol=%.6f",
		coarse.Price, fine.Price, math.Abs(coarse.Price-fine.Price), tol)
	if math.Abs(coarse.Price-fine.Price) > tol {
		t.Fatalf("asian price did not converge under step refinement")
	}
}

func TestBarrierInOutParity(t *testing.T) {
	mk := func(kind BarrierKind) Option {
		o := baseOption()
		o.Type = Barrier
		o.Barrier = BarrierSpec{Level: 130, Direction: Up, Kind: kind, Monitoring: MonitorDiscrete}
		return o
	}
	cfg := baseConfig()
	eng := NewEngine()
	out, err := eng.Price(mk(KnockOut), cfg)
	if err != nil {
		t.Fatal(err)
	}
	in, err := eng.Price(mk(KnockIn), cfg)
	if err != nil {
		t.Fatal(err)
	}
	vanilla, err := eng.Price(baseOption(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("out=%.6f in=%.6f sum=%.6f vanilla=%.6f", out.Price, in.Price, out.Price+in.Price, vanilla.Price)
	// Parity holds pathwise; allow only float accumulation noise.
	if math.Abs(out.Price+in.Price-vanilla.Price) > 1e-9*vanilla.Price {
		t.Fatalf("in-out parity violated pathwise: %f + %f != %f", out.Price, in.Price, vanilla.Price)
	}
}

func TestBarrierMonitoringSemantics(t *testing.T) {
	mk := func(m Monitoring, steps int) Option {
		o := baseOption()
		o.Type = Barrier
		o.Barrier = BarrierSpec{Level: 115, Direction: Up, Kind: KnockOut, Monitoring: m}
		_ = steps
		return o
	}
	eng := NewEngine()
	disc, err := eng.Price(mk(MonitorDiscrete, 8), Config{NumPaths: 40000, NumSteps: 8, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	cont, err := eng.Price(mk(MonitorContinuous, 8), Config{NumPaths: 40000, NumSteps: 8, Seed: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("discrete KO=%.6f continuous KO=%.6f", disc.Price, cont.Price)
	// Continuous monitoring knocks out more often, so the knock-out
	// call must be cheaper.
	if cont.Price >= disc.Price {
		t.Fatalf("continuous KO (%f) should be cheaper than discrete KO (%f)", cont.Price, disc.Price)
	}
}

func TestAntitheticVarianceReduction(t *testing.T) {
	o := baseOption()
	o.Type = Asian
	eng := NewEngine()
	plain, err := eng.Price(o, Config{NumPaths: 20000, NumSteps: 64, Seed: 11})
	if err != nil {
		t.Fatal(err)
	}
	anti, err := eng.Price(o, Config{NumPaths: 20000, NumSteps: 64, Seed: 11, Antithetic: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plain: price=%.6f se=%.6f | antithetic: price=%.6f se=%.6f",
		plain.Price, plain.StdErr, anti.Price, anti.StdErr)
	if anti.StdErr >= plain.StdErr {
		t.Fatalf("antithetic stderr %f not below plain %f", anti.StdErr, plain.StdErr)
	}
	if math.Abs(anti.Price-plain.Price) > 3*(plain.StdErr+anti.StdErr) {
		t.Fatalf("antithetic price %f drifted from plain %f", anti.Price, plain.Price)
	}
}

func TestDeterminismAcrossWorkers(t *testing.T) {
	opts := make([]Option, 12)
	for i := range opts {
		o := baseOption()
		o.Strike = 80 + float64(i)*5
		switch i % 3 {
		case 1:
			o.Type = Asian
		case 2:
			o.Type = Barrier
			o.Barrier = BarrierSpec{Level: 140, Direction: Up, Kind: KnockOut, Monitoring: MonitorContinuous}
		}
		opts[i] = o
	}
	cfg := Config{NumPaths: 4000, NumSteps: 32, Seed: 99}
	eng := NewEngine()
	serial := eng.PriceBatch(opts, withWorkers(cfg, 1))
	parallel := eng.PriceBatch(opts, withWorkers(cfg, 8))
	for i := range serial {
		if serial[i].Err != nil || parallel[i].Err != nil {
			t.Fatalf("task %d err: %v / %v", i, serial[i].Err, parallel[i].Err)
		}
		if serial[i].Result != parallel[i].Result {
			t.Fatalf("task %d differs across worker counts: %+v vs %+v",
				i, serial[i].Result, parallel[i].Result)
		}
	}
	t.Logf("12 tasks bit-identical between 1 and 8 workers")

	// Shuffled input order must not change any option's result.
	shuffled := make([]Option, len(opts))
	perm := []int{5, 0, 11, 3, 7, 1, 9, 2, 10, 4, 8, 6}
	for dst, src := range perm {
		shuffled[dst] = opts[src]
	}
	out2 := eng.PriceBatch(shuffled, withWorkers(cfg, 4))
	for dst, src := range perm {
		if out2[dst].Result != serial[src].Result {
			t.Fatalf("option moved %d->%d changed result", src, dst)
		}
	}
	t.Logf("results independent of task order")

	// A different seed must change the numbers.
	other := eng.PriceBatch(opts, Config{NumPaths: 4000, NumSteps: 32, Seed: 100, MaxWorkers: 8})
	same := 0
	for i := range other {
		if other[i].Result.Price == serial[i].Result.Price {
			same++
		}
	}
	if same != 0 {
		t.Fatalf("%d results identical across different seeds", same)
	}
}

func withWorkers(c Config, w int) Config {
	c.MaxWorkers = w
	return c
}

func TestTooFewPaths(t *testing.T) {
	_, err := NewEngine().Price(baseOption(), Config{NumPaths: 10, NumSteps: 8, Seed: 1})
	if kindOf(err) != ErrTooFewPaths {
		t.Fatalf("want ErrTooFewPaths, got %v", err)
	}
}

func TestNotConverged(t *testing.T) {
	cfg := Config{NumPaths: 2000, NumSteps: 8, Seed: 1, RelTol: 1e-9}
	_, err := NewEngine().Price(baseOption(), cfg)
	if kindOf(err) != ErrNotConverged {
		t.Fatalf("want ErrNotConverged, got %v", err)
	}
}

func TestNonFinite(t *testing.T) {
	o := baseOption()
	o.Vol = 1e200
	_, err := NewEngine().Price(o, Config{NumPaths: 2000, NumSteps: 8, Seed: 1})
	if kindOf(err) != ErrNonFinite {
		t.Fatalf("want ErrNonFinite, got %v", err)
	}
}

func TestGreeksStability(t *testing.T) {
	o := baseOption()
	cfg := Config{NumPaths: 20000, NumSteps: 32, Seed: 5}
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
	// Analytic BS delta for the call: exp(-qT) N(d1).
	sqrtT := math.Sqrt(o.Maturity)
	d1 := (math.Log(o.Spot/o.Strike) + (o.Rate-o.DivYield+0.5*o.Vol*o.Vol)*o.Maturity) / (o.Vol * sqrtT)
	bsDelta := math.Exp(-o.DivYield*o.Maturity) * normCDF(d1)
	t.Logf("delta=%.4f (bs %.4f) gamma=%.6f vega=%.4f theta=%.4f rho=%.4f",
		g1.Delta, bsDelta, g1.Gamma, g1.Vega, g1.Theta, g1.Rho)
	if math.Abs(g1.Delta-bsDelta) > 0.05 {
		t.Fatalf("delta %f too far from BS %f", g1.Delta, bsDelta)
	}
	if g1.Gamma <= 0 || g1.Vega <= 0 {
		t.Fatalf("ATM call gamma/vega should be positive: %+v", g1)
	}
}
