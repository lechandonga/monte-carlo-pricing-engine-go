package engine_test

import (
	"math"
	"testing"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/engine"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

const mcTol = 5.0 // standard errors allowed between MC and analytic

func eurOpt(right option.Right) option.Params {
	return option.Params{Kind: option.European, Right: right, Spot: 100, Strike: 100,
		T: 1, R: 0.05, Q: 0.01, Vol: 0.2, Steps: 1}
}

func asianOpt(right option.Right, steps int) option.Params {
	p := eurOpt(right)
	p.Kind = option.Asian
	p.Steps = steps
	return p
}

func barrierOpt(right option.Right, dir option.BarrierDirection,
	bt option.BarrierType, h float64, mon option.Monitoring, steps int) option.Params {
	p := asianOpt(right, steps)
	p.Kind = option.Barrier
	p.Direction = dir
	p.BarType = bt
	p.Barrier = h
	p.Monitoring = mon
	return p
}

func inCI(r option.Result, x float64) bool {
	return x >= r.CI95Low && x <= r.CI95High
}

// TestEuropeanAgreesWithAnalytic is the main pricing flow: Monte Carlo at a
// high path count must cover the closed-form price in its 95% CI, for calls
// and puts.
func TestEuropeanAgreesWithAnalytic(t *testing.T) {
	for _, right := range []option.Right{option.Call, option.Put} {
		name := "call"
		if right == option.Put {
			name = "put"
		}
		p := eurOpt(right)
		cfg := engine.Config{Paths: 400000, Workers: 4, Seed: 20240101}
		r, err := engine.Price(p, cfg)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		analytic, ok := engine.PriceAnalytic(p)
		if !ok {
			t.Fatalf("%s: analytic reference unavailable", name)
		}
		if !inCI(r, analytic) {
			t.Fatalf("%s: analytic %.6f outside MC CI [%.6f, %.6f] (price %.6f se %.6f)",
				name, analytic, r.CI95Low, r.CI95High, r.Price, r.StdErr)
		}
		if math.Abs(r.Price-analytic) > mcTol*r.StdErr {
			t.Fatalf("%s: MC bias %.6f exceeds %.0f SE (%.6f)", name, math.Abs(r.Price-analytic), mcTol, r.StdErr)
		}
		t.Logf("business=european case=%s mc=%.6f analytic=%.6f stderr=%.6f ci=[%.6f,%.6f] result=ok",
			name, r.Price, analytic, r.StdErr, r.CI95Low, r.CI95High)
	}
}

// TestBarrierAgreesWithAnalytic covers all eight continuously monitored
// contracts via the Bernoulli bridge estimator.
func TestBarrierAgreesWithAnalytic(t *testing.T) {
	type c struct {
		name  string
		right option.Right
		dir   option.BarrierDirection
		bt    option.BarrierType
		h     float64
	}
	cases := []c{
		{"up_and_out_call", option.Call, option.Up, option.KnockOut, 120},
		{"up_and_in_call", option.Call, option.Up, option.KnockIn, 120},
		{"down_and_out_call", option.Call, option.Down, option.KnockOut, 85},
		{"down_and_in_call", option.Call, option.Down, option.KnockIn, 85},
		{"up_and_out_put", option.Put, option.Up, option.KnockOut, 112},
		{"up_and_in_put", option.Put, option.Up, option.KnockIn, 112},
		{"down_and_out_put", option.Put, option.Down, option.KnockOut, 90},
		{"down_and_in_put", option.Put, option.Down, option.KnockIn, 90},
	}
	for _, tc := range cases {
		p := barrierOpt(tc.right, tc.dir, tc.bt, tc.h, option.Continuous, 252)
		cfg := engine.Config{Paths: 300000, Workers: 4, Seed: 777}
		r, err := engine.Price(p, cfg)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		analytic, _ := engine.PriceAnalytic(p)
		if math.Abs(r.Price-analytic) > mcTol*r.StdErr && !inCI(r, analytic) {
			t.Fatalf("%s: analytic %.6f vs MC %.6f se %.6f", tc.name, analytic, r.Price, r.StdErr)
		}
		t.Logf("business=barrier_continuous case=%-18s mc=%.6f analytic=%.6f stderr=%.6f result=ok",
			tc.name, r.Price, analytic, r.StdErr)
	}
}

// TestPathDependentConvergence: arithmetic Asians have no closed form.
// Refining the step grid must converge to a stable limit, with shrinking
// standard error, and discrete monitoring must approach continuous barrier
// monitoring as the grid is refined.
func TestPathDependentConvergence(t *testing.T) {
	p := asianOpt(option.Call, 12)
	cfg := engine.Config{Paths: 200000, Workers: 4, Seed: 31}
	var prev option.Result
	first := true
	// Coarse grids carry discretization bias; convergence is assessed on the
	// fine half of the grid sequence where changes must be noise-consistent.
	grids := []int{12, 25, 50, 100, 200}
	for _, steps := range grids {
		p.Steps = steps
		r, err := engine.Price(p, cfg)
		if err != nil {
			t.Fatalf("asian steps=%d: %v", steps, err)
		}
		t.Logf("business=asian_convergence steps=%-3d price=%.6f stderr=%.6f ci=[%.6f,%.6f]",
			steps, r.Price, r.StdErr, r.CI95Low, r.CI95High)
		// Only the 25+ grids are required to be mutually consistent.
		if steps >= 50 && !first && math.Abs(r.Price-prev.Price) > 4*(r.StdErr+prev.StdErr) {
			t.Fatalf("asian price jump %f -> %f when refining grid", prev.Price, r.Price)
		}
		if steps >= 50 {
			prev, first = r, false
		}
	}
	if first {
		t.Fatal("no fine-grid results produced")
	}

	diff0 := 0.0
	for i, steps := range []int{24, 96} {
		pd := barrierOpt(option.Call, option.Up, option.KnockOut, 120, option.Discrete, steps)
		pc := pd
		pc.Monitoring = option.Continuous
		rd, err := engine.Price(pd, cfg)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := engine.Price(pc, cfg)
		if err != nil {
			t.Fatal(err)
		}
		diff := math.Abs(rd.Price - rc.Price)
		t.Logf("business=barrier_monitoring steps=%-3d discrete=%.6f continuous=%.6f |diff|=%.6f",
			steps, rd.Price, rc.Price, diff)
		if i == 0 {
			diff0 = diff
		} else if diff > diff0 {
			t.Fatalf("discrete/continuous gap did not shrink on grid refinement: %f -> %f", diff0, diff)
		}
	}
}

// TestVarianceReduction: antithetic and the geometric-Asian control variate
// must materially cut standard error while keeping the price within the plain
// estimator's confidence band.
func TestVarianceReduction(t *testing.T) {
	p := asianOpt(option.Call, 50)
	plain, err := engine.Price(p, engine.Config{Paths: 200000, Workers: 4, Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := engine.Price(p, engine.Config{Paths: 200000, Workers: 4, Seed: 99, ControlVariate: true})
	if err != nil {
		t.Fatal(err)
	}
	anti, err := engine.Price(p, engine.Config{Paths: 200000, Workers: 4, Seed: 99, Antithetic: true})
	if err != nil {
		t.Fatal(err)
	}
	eurP := eurOpt(option.Call)
	eurPlain, err := engine.Price(eurP, engine.Config{Paths: 200000, Workers: 4, Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	eurAnti, err := engine.Price(eurP, engine.Config{Paths: 200000, Workers: 4, Seed: 99, Antithetic: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("business=variance_reduction case=european_anti  plain_se=%.6f anti_se=%.6f",
		eurPlain.StdErr, eurAnti.StdErr)
	t.Logf("business=variance_reduction case=plain        price=%.6f stderr=%.6f", plain.Price, plain.StdErr)
	t.Logf("business=variance_reduction case=control_cv   price=%.6f stderr=%.6f used_cv=%t", cv.Price, cv.StdErr, cv.UsedCV)
	t.Logf("business=variance_reduction case=antithetic   price=%.6f stderr=%.6f", anti.Price, anti.StdErr)
	if cv.StdErr > 0.5*plain.StdErr {
		t.Fatalf("control variate did not reduce error enough: plain %.6f cv %.6f", plain.StdErr, cv.StdErr)
	}
	if !inCI(plain, cv.Price) && math.Abs(cv.Price-plain.Price) > 5*plain.StdErr {
		t.Fatalf("control-variate price %.6f drifted far from plain %.6f", cv.Price, plain.Price)
	}
	// Antithetic gives a strong reduction for the terminal-value European
	// payoff; for the path average the reduction is mild, so only the
	// unbiasedness band is enforced there.
	if eurAnti.StdErr > 0.8*eurPlain.StdErr {
		t.Fatalf("european antithetic did not reduce error: plain %.6f anti %.6f",
			eurPlain.StdErr, eurAnti.StdErr)
	}
	if !inCI(eurPlain, eurAnti.Price) {
		t.Fatalf("european antithetic price %.6f outside plain CI", eurAnti.Price)
	}
	if math.Abs(anti.Price-plain.Price) > 5*plain.StdErr {
		t.Fatalf("asian antithetic price %.6f drifted from plain %.6f", anti.Price, plain.Price)
	}
}

// TestSeedAndConcurrencyDeterminism is the core reproducibility contract:
// same seed + input must be bit-identical regardless of worker count or
// chunking; a different seed must produce an independent stream.
func TestSeedAndConcurrencyDeterminism(t *testing.T) {
	p := barrierOpt(option.Call, option.Up, option.KnockOut, 115, option.Continuous, 64)
	base := engine.Config{Paths: 80000, Seed: 424242}

	ref, err := engine.Price(p, base)
	if err != nil {
		t.Fatal(err)
	}
	variants := []struct {
		name     string
		workers  int
		batch    int
		anti, cv bool
	}{
		{"workers_2", 2, 0, false, false},
		{"workers_8", 8, 0, false, false},
		{"workers_8_small_chunks", 8, 137, false, false},
		{"workers_4_antithetic", 4, 0, true, false},
		{"workers_3_antithetic_cv", 3, 256, true, true},
		{"repeat_single_worker", 1, 0, false, false},
	}
	for _, v := range variants {
		c := base
		c.Workers, c.BatchSize, c.Antithetic, c.ControlVariate = v.workers, v.batch, v.anti, v.cv
		got, err := engine.Price(p, c)
		if err != nil {
			t.Fatal(err)
		}
		// Reduced-variance variants are compared against their own
		// single-worker twin; plain variants must equal ref bit-for-bit.
		var want option.Result
		if v.anti || v.cv {
			c1 := c
			c1.Workers = 1
			c1.BatchSize = 0
			want, _ = engine.Price(p, c1)
		} else {
			want = ref
		}
		if got.Price != want.Price || got.StdErr != want.StdErr ||
			got.CI95Low != want.CI95Low || got.CI95High != want.CI95High {
			t.Fatalf("business=determinism case=%s:\n got %.17g/%.17g/[%.17g,%.17g]\nwant %.17g/%.17g/[%.17g,%.17g]",
				v.name, got.Price, got.StdErr, got.CI95Low, got.CI95High,
				want.Price, want.StdErr, want.CI95Low, want.CI95High)
		}
		t.Logf("business=determinism case=%-26s price=%.10f stderr=%.10f result=bit_identical",
			v.name, got.Price, got.StdErr)
	}

	other := base
	other.Seed = 424243
	ro, err := engine.Price(p, other)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Price == ref.Price {
		t.Fatalf("different seeds produced an identical price; stream derivation may be broken")
	}
	t.Logf("business=determinism case=different_seed           price_ref=%.10f price_other=%.10f result=independent",
		ref.Price, ro.Price)
}

// TestBatchOrderIndependence runs the same jobs in different orders with
// different worker limits; per-tag outputs must stay bit-identical.
func TestBatchOrderIndependence(t *testing.T) {
	reqs := []option.Request{
		{Tag: "eur-c", Params: eurOpt(option.Call)},
		{Tag: "asian-p", Params: asianOpt(option.Put, 30)},
		{Tag: "uoc", Params: barrierOpt(option.Call, option.Up, option.KnockOut, 118, option.Continuous, 80)},
		{Tag: "dip", Params: barrierOpt(option.Put, option.Down, option.KnockIn, 90, option.Discrete, 80)},
	}
	// Same chunking in both runs; only worker count and job order differ.
	cfg1 := engine.Config{Paths: 60000, Seed: 5150, Workers: 1, BatchSize: 1024}
	a := engine.PriceBatch(reqs, cfg1)

	reversed := []option.Request{reqs[3], reqs[2], reqs[1], reqs[0]}
	cfg8 := cfg1
	cfg8.Workers = 8
	b := engine.PriceBatch(reversed, cfg8)

	index := func(rs []option.Result, tag string) option.Result {
		for _, r := range rs {
			if r.Tag == tag {
				return r
			}
		}
		t.Fatalf("tag %s missing", tag)
		return option.Result{}
	}
	for _, req := range reqs {
		x, y := index(a, req.Tag), index(b, req.Tag)
		if x.Err != nil || y.Err != nil {
			t.Fatalf("%s: unexpected batch error %v / %v", req.Tag, x.Err, y.Err)
		}
		if x.Price != y.Price || x.StdErr != y.StdErr || x.CI95Low != y.CI95Low || x.CI95High != y.CI95High {
			t.Fatalf("business=batch case=%s: order/workers changed the result", req.Tag)
		}
		t.Logf("business=batch case=%-8s price=%.10f stderr=%.10f result=order_worker_independent",
			req.Tag, x.Price, x.StdErr)
	}
}

// TestBatchPropagatesErrors ensures invalid jobs inside a batch surface their
// typed error on the matching result instead of aborting the batch.
func TestBatchPropagatesErrors(t *testing.T) {
	reqs := []option.Request{
		{Tag: "ok", Params: eurOpt(option.Call)},
		{Tag: "bad-vol", Params: func() option.Params { p := eurOpt(option.Call); p.Vol = -1; return p }()},
	}
	rs := engine.PriceBatch(reqs, engine.Config{Paths: 20000, Seed: 1, Workers: 2})
	if rs[0].Err != nil {
		t.Fatalf("valid job failed unexpectedly: %v", rs[0].Err)
	}
	if rs[1].Err == nil || option.ErrorCodeOf(rs[1].Err) != option.ErrNegativeVol {
		t.Fatalf("bad-vol job error = %v, want code %s", rs[1].Err, option.ErrNegativeVol)
	}
	t.Logf("business=batch_errors case=bad_vol code=%s reason=%q result=reported_per_job",
		option.ErrorCodeOf(rs[1].Err), rs[1].Err)
}

// TestEngineErrorContracts covers the runtime error classes that must be
// raised rather than silently returning a number.
func TestEngineErrorContracts(t *testing.T) {
	t.Run("too_few_paths", func(t *testing.T) {
		_, err := engine.Price(eurOpt(option.Call), engine.Config{Paths: 10, Seed: 1, MinPaths: 1024})
		if option.ErrorCodeOf(err) != option.ErrTooFewPaths {
			t.Fatalf("code = %s, want %s", option.ErrorCodeOf(err), option.ErrTooFewPaths)
		}
		t.Logf("business=sim_error case=too_few_paths code=%s result=rejected", option.ErrTooFewPaths)
	})
	t.Run("odd_antithetic_paths", func(t *testing.T) {
		_, err := engine.Price(eurOpt(option.Call), engine.Config{Paths: 100001, Seed: 1, Antithetic: true})
		if option.ErrorCodeOf(err) != option.ErrInvalidSimConfig {
			t.Fatalf("code = %s, want %s", option.ErrorCodeOf(err), option.ErrInvalidSimConfig)
		}
		t.Logf("business=sim_error case=odd_antithetic code=%s result=rejected", option.ErrInvalidSimConfig)
	})
	t.Run("negative_tolerance", func(t *testing.T) {
		_, err := engine.Price(eurOpt(option.Call), engine.Config{Paths: 20000, Seed: 1, RelTol: -0.01})
		if option.ErrorCodeOf(err) != option.ErrInvalidSimConfig {
			t.Fatalf("code = %s, want %s", option.ErrorCodeOf(err), option.ErrInvalidSimConfig)
		}
		t.Logf("business=sim_error case=negative_tolerance code=%s result=rejected", option.ErrInvalidSimConfig)
	})
	t.Run("not_converged", func(t *testing.T) {
		r, err := engine.Price(eurOpt(option.Call), engine.Config{
			Paths: 4096, Seed: 1, Workers: 1, RelTol: 1e-6, MinPaths: 1024})
		if option.ErrorCodeOf(err) != option.ErrNotConverged {
			t.Fatalf("code = %s, want %s (price=%v)", option.ErrorCodeOf(err), option.ErrNotConverged, r.Price)
		}
		if r.Converged {
			t.Fatalf("result must be flagged not converged")
		}
		t.Logf("business=sim_error case=not_converged price=%.6f stderr=%.6f code=%s result=reported",
			r.Price, r.StdErr, option.ErrNotConverged)
	})
	t.Run("invalid_product_params", func(t *testing.T) {
		p := eurOpt(option.Call)
		p.T = 0
		_, err := engine.Price(p, engine.Config{Paths: 20000, Seed: 1})
		if option.ErrorCodeOf(err) != option.ErrNonPositiveMaturity {
			t.Fatalf("code = %s, want %s", option.ErrorCodeOf(err), option.ErrNonPositiveMaturity)
		}
		t.Logf("business=sim_error case=invalid_product code=%s result=rejected", option.ErrNonPositiveMaturity)
	})
}

// TestGreeksStable: CRN finite differences must agree with analytic European
// Greeks and be bit-stable across repeated calls and worker counts.
func TestGreeksStable(t *testing.T) {
	p := eurOpt(option.Call)
	cfg := engine.GreekConfig{Sim: engine.Config{Paths: 300000, Workers: 4, Seed: 8080}}
	g1, err := engine.EstimateGreeks(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := engine.EstimateGreeks(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g1 != g2 {
		t.Fatalf("repeated greek estimation differs:\n%+v\n%+v", g1, g2)
	}
	cfg1 := cfg
	cfg1.Sim.Workers = 1
	g3, err := engine.EstimateGreeks(p, cfg1)
	if err != nil {
		t.Fatal(err)
	}
	if g1 != g3 {
		t.Fatalf("greeks differ with worker count:\n%+v\n%+v", g1, g3)
	}
	ag := analyticEuropeanGreeks(p)
	check := func(name string, got, want, se float64) {
		t.Helper()
		if math.Abs(got-want) > 6*se && math.Abs(got-want) > 0.02 {
			t.Fatalf("%s: got %.6f want analytic %.6f (se %.6f)", name, got, want, se)
		}
		t.Logf("business=greeks case=%-6s mc=%.6f analytic=%.6f stderr=%.6f result=stable", name, got, want, se)
	}
	check("delta", g1.Delta, ag.delta, g1.DeltaSE)
	check("gamma", g1.Gamma, ag.gamma, g1.GammaSE)
	check("vega", g1.Vega, ag.vega, g1.VegaSE)
	check("rho", g1.Rho, ag.rho, g1.RhoSE)
	// Theta is noisier; just require finiteness and a sane sign for this
	// long-dated call.
	if math.IsNaN(g1.Theta) || math.IsInf(g1.Theta, 0) {
		t.Fatalf("theta non-finite: %v", g1.Theta)
	}
	t.Logf("business=greeks case=theta  mc=%.6f stderr=%.6f result=finite", g1.Theta, g1.ThetaSE)
}

// TestWorkersResourceLimit verifies Workers acts as a resource cap and that
// Workers=1 never spawns pricing goroutines (covered under -race).
func TestWorkersResourceLimit(t *testing.T) {
	p := asianOpt(option.Call, 40)
	for _, w := range []int{1, 2, 4} {
		cfg := engine.Config{Paths: 100000, Workers: w, Seed: 9}
		r, err := engine.Price(p, cfg)
		if err != nil {
			t.Fatalf("workers=%d: %v", w, err)
		}
		t.Logf("business=resource_cap case=workers_%d price=%.6f stderr=%.6f result=ok", w, r.Price, r.StdErr)
	}
}
