package engine

import (
	"math"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

// EstimateGreeks computes Monte Carlo sensitivities by central finite
// differences. Every bumped price uses the same seed, and because each path
// index owns its random stream, all prices are evaluated on identical
// randomness (common random numbers). This keeps repeated estimates
// bit-stable: running the function twice on the same input returns the same
// values exactly, and the differencing noise is far below independent-path
// noise.
func EstimateGreeks(p option.Params, cfg GreekConfig) (Greeks, error) {
	if err := cfg.Sim.validate(p); err != nil {
		return Greeks{}, err
	}
	bs := cfg.BumpSpot
	if bs <= 0 {
		bs = 0.01 * p.Spot
	}
	bv := cfg.BumpVol
	if bv <= 0 {
		bv = 0.01
	}
	br := cfg.BumpRate
	if br <= 0 {
		br = 0.0001
	}
	bt := cfg.BumpTime
	if bt <= 0 {
		bt = 1.0 / 365.0
	}

	priceOf := func(q option.Params) (option.Result, error) { return Price(q, cfg.Sim) }

	// Delta / Gamma on a spot grid p.Spot - bs, p.Spot, p.Spot + bs.
	up := p
	up.Spot = p.Spot + bs
	down := p
	down.Spot = p.Spot - bs
	rUp, err := priceOf(up)
	if err != nil {
		return Greeks{}, err
	}
	rDown, err := priceOf(down)
	if err != nil {
		return Greeks{}, err
	}
	rMid, err := priceOf(p)
	if err != nil {
		return Greeks{}, err
	}

	g := Greeks{
		Delta:   (rUp.Price - rDown.Price) / (2 * bs),
		DeltaSE: sqrtSE(rUp.StdErr, rDown.StdErr) / (2 * bs),
		Gamma:   (rUp.Price - 2*rMid.Price + rDown.Price) / (bs * bs),
		GammaSE: sqrtSE3(rUp.StdErr, rMid.StdErr, rDown.StdErr) / (bs * bs),
	}

	// Vega.
	vUp, vDown, vUpSE, vDownSE, err := bumpVol(p, cfg.Sim, bv)
	if err != nil {
		return Greeks{}, err
	}
	g.Vega = (vUp - vDown) / (2 * bv)
	g.VegaSE = sqrtSE(vUpSE, vDownSE) / (2 * bv)

	// Rho.
	rrUp, rrDown, rrUpSE, rrDownSE, err := bumpRate(p, cfg.Sim, br)
	if err != nil {
		return Greeks{}, err
	}
	g.Rho = (rrUp - rrDown) / (2 * br)
	g.RhoSE = sqrtSE(rrUpSE, rrDownSE) / (2 * br)

	// Theta: backward difference in maturity (value gained per year as t
	// shrinks, reported as dV/dt using T - 1d vs T).
	earlier := p
	earlier.T = p.T - bt
	earlier.Steps = max(1, p.Steps-1)
	rEarlier, err := Price(earlier, cfg.Sim)
	if err != nil {
		return Greeks{}, err
	}
	g.Theta = -(rMid.Price - rEarlier.Price) / bt
	g.ThetaSE = sqrtSE(rMid.StdErr, rEarlier.StdErr) / bt

	return g, nil
}

func bumpVol(p option.Params, cfg Config, bv float64) (up, down, upSE, downSE float64, err error) {
	u := p
	u.Vol = p.Vol + bv
	d := p
	d.Vol = p.Vol - bv
	if d.Vol < 0 {
		d.Vol = 0
	}
	ru, e := Price(u, cfg)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	rd, e := Price(d, cfg)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	return ru.Price, rd.Price, ru.StdErr, rd.StdErr, nil
}

func bumpRate(p option.Params, cfg Config, br float64) (up, down, upSE, downSE float64, err error) {
	u := p
	u.R = p.R + br
	d := p
	d.R = p.R - br
	ru, e := Price(u, cfg)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	rd, e := Price(d, cfg)
	if e != nil {
		return 0, 0, 0, 0, e
	}
	return ru.Price, rd.Price, ru.StdErr, rd.StdErr, nil
}

// sqrtSE is the standard error of (a-b) when a,b are CRN-independent
// variances across independent estimators: sqrt(se_a^2 + se_b^2).
func sqrtSE(a, b float64) float64 {
	x := a*a + b*b
	if x < 0 {
		return 0
	}
	return math.Sqrt(x)
}

func sqrtSE3(a, m, b float64) float64 {
	x := a*a + 4*m*m + b*b
	if x < 0 {
		return 0
	}
	return math.Sqrt(x)
}
