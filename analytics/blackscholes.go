// Package analytics provides closed-form reference prices used both as
// user-facing analytical results and as Monte Carlo control variates and
// test oracles.
package analytics

import "math"

// Phi is the standard normal cumulative distribution function.
func Phi(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }

// pdf of the standard normal.
func phi(x float64) float64 { return math.Exp(-x*x/2) / math.Sqrt(2*math.Pi) }

// BlackScholes prices a European call or put with continuous dividend yield.
// vol must be non-negative; vol == 0 is handled as the intrinsic forward
// value.
func BlackScholes(spot, strike, t, r, q, vol float64, call bool) float64 {
	fwdDiscount := math.Exp(-r * t)
	if vol == 0 {
		forward := spot * math.Exp((r-q)*t)
		if call {
			return fwdDiscount * math.Max(forward-strike, 0)
		}
		return fwdDiscount * math.Max(strike-forward, 0)
	}
	d1 := (math.Log(spot/strike) + (r-q+vol*vol/2)*t) / (vol * math.Sqrt(t))
	d2 := d1 - vol*math.Sqrt(t)
	if call {
		return spot*math.Exp(-q*t)*Phi(d1) - strike*fwdDiscount*Phi(d2)
	}
	return strike*fwdDiscount*Phi(-d2) - spot*math.Exp(-q*t)*Phi(-d1)
}

// Greeks holds closed-form European sensitivities.
type Greeks struct {
	Delta float64
	Gamma float64
	Vega  float64
	Rho   float64
	Theta float64
}

// BlackScholesGreeks returns analytic European Greeks. Vega is per 1.0 vol
// (not per percentage point); Theta is per year.
func BlackScholesGreeks(spot, strike, t, r, q, vol float64, call bool) Greeks {
	if vol == 0 {
		return Greeks{}
	}
	discount := math.Exp(-r * t)
	dq := math.Exp(-q * t)
	sqrtT := math.Sqrt(t)
	d1 := (math.Log(spot/strike) + (r-q+vol*vol/2)*t) / (vol * sqrtT)
	d2 := d1 - vol*sqrtT
	g := Greeks{
		Gamma: dq * phi(d1) / (spot * vol * sqrtT),
		Vega:  spot * dq * phi(d1) * sqrtT,
	}
	if call {
		g.Delta = dq * Phi(d1)
		g.Rho = strike * t * discount * Phi(d2)
		g.Theta = -spot*dq*phi(d1)*vol/(2*sqrtT) - r*strike*discount*Phi(d2) + q*spot*dq*Phi(d1)
	} else {
		g.Delta = -dq * Phi(-d1)
		g.Rho = -strike * t * discount * Phi(-d2)
		g.Theta = -spot*dq*phi(d1)*vol/(2*sqrtT) + r*strike*discount*Phi(-d2) - q*spot*dq*Phi(-d1)
	}
	return g
}
