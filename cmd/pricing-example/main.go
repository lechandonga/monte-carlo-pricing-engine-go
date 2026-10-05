// Command pricing-example demonstrates the fully local pricing flow: a
// European option against its analytic value, a continuously monitored
// barrier option, an arithmetic Asian with the control variate, parallel
// batch pricing, and CRN Greeks. No network access or external data is used.
package main

import (
	"fmt"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/engine"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

func main() {
	// 1. European call: Monte Carlo vs closed form.
	eur := option.Params{
		Kind: option.European, Right: option.Call,
		Spot: 100, Strike: 100, T: 1,
		R: 0.05, Q: 0, Vol: 0.2, Steps: 1,
	}
	res, err := engine.Price(eur, engine.Config{Paths: 400000, Workers: 4, Seed: 20240101})
	must(err)
	analytic, _ := engine.PriceAnalytic(eur)
	fmt.Printf("European call : MC=%.4f SE=%.4f CI=[%.4f,%.4f] analytic=%.4f\n",
		res.Price, res.StdErr, res.CI95Low, res.CI95High, analytic)

	// 2. Up-and-out call with continuous monitoring (Bernoulli bridge).
	uoc := eur
	uoc.Kind = option.Barrier
	uoc.Steps = 252
	uoc.Direction = option.Up
	uoc.BarType = option.KnockOut
	uoc.Barrier = 120
	uoc.Monitoring = option.Continuous
	res, err = engine.Price(uoc, engine.Config{Paths: 400000, Workers: 4, Seed: 42})
	must(err)
	analytic, _ = engine.PriceAnalytic(uoc)
	fmt.Printf("Up-and-out call: MC=%.4f SE=%.4f analytic=%.4f\n", res.Price, res.StdErr, analytic)

	// 3. Arithmetic Asian: antithetic + geometric-Asian control variate.
	asian := eur
	asian.Kind = option.Asian
	asian.Steps = 50
	plain, err := engine.Price(asian, engine.Config{Paths: 200000, Workers: 4, Seed: 7})
	must(err)
	cv, err := engine.Price(asian, engine.Config{
		Paths: 200000, Workers: 4, Seed: 7,
		Antithetic: true, ControlVariate: true,
	})
	must(err)
	fmt.Printf("Asian call    : plain=%.4f SE=%.4f | VR=%.4f SE=%.4f\n",
		plain.Price, plain.StdErr, cv.Price, cv.StdErr)

	// 4. Determinism: same seed, any worker count -> identical bytes.
	a, _ := engine.Price(uoc, engine.Config{Paths: 100000, Workers: 1, Seed: 99})
	b, _ := engine.Price(uoc, engine.Config{Paths: 100000, Workers: 8, Seed: 99})
	fmt.Printf("Determinism   : 1 worker %.10f == 8 workers %.10f -> %v\n",
		a.Price, b.Price, a.Price == b.Price && a.StdErr == b.StdErr)

	// 5. Parallel batch with per-job order-independent (tagged) seeds.
	reqs := []option.Request{
		{Tag: "eur-call", Params: eur},
		{Tag: "asian-put", Params: func() option.Params {
			p := asian
			p.Right = option.Put
			return p
		}()},
		{Tag: "uoc", Params: uoc},
	}
	results := engine.PriceBatch(reqs, engine.Config{
		Paths: 100000, Workers: 2, Seed: 1234, BatchSize: 4096,
	})
	for _, r := range results {
		fmt.Printf("Batch %-10s: price=%.4f SE=%.4f err=%v\n", r.Tag, r.Price, r.StdErr, r.Err)
	}

	// 6. Common-random-number Greeks.
	g, err := engine.EstimateGreeks(eur, engine.GreekConfig{
		Sim: engine.Config{Paths: 300000, Workers: 4, Seed: 8080},
	})
	must(err)
	fmt.Printf("Greeks        : delta=%.4f gamma=%.5f vega=%.4f rho=%.4f theta=%.4f\n",
		g.Delta, g.Gamma, g.Vega, g.Rho, g.Theta)

	// 7. Invalid input is rejected with a typed, distinguishable error.
	bad := eur
	bad.Vol = -0.2
	if _, err := engine.Price(bad, engine.Config{Paths: 10000, Seed: 1}); err != nil {
		fmt.Printf("Rejected      : code=%s reason=%v\n", option.ErrorCodeOf(err), err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
