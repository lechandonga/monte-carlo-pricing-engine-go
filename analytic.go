package mcengine

import "math"

// normCDF is the standard normal cumulative distribution function.
func normCDF(x float64) float64 {
	return 0.5 * (1 + math.Erf(x/math.Sqrt2))
}

// normInv is the inverse standard normal CDF (Acklam's rational
// approximation, refined with one Halley step).
func normInv(p float64) float64 {
	const (
		a1 = -3.969683028665376e+01
		a2 = 2.209460984245205e+02
		a3 = -2.759285104469687e+02
		a4 = 1.383577518672690e+02
		a5 = -3.066479806614716e+01
		a6 = 2.506628277459239e+00
		b1 = -5.447609879822406e+01
		b2 = 1.615858368580409e+02
		b3 = -1.556989798598866e+02
		b4 = 6.680131188771972e+01
		b5 = -1.328068155288572e+01
		c1 = -7.784894002430293e-03
		c2 = -3.223964580411365e-01
		c3 = -2.400758277161838e+00
		c4 = -2.549732539343734e+00
		c5 = 4.374664141464968e+00
		c6 = 2.938163982698783e+00
		d1 = 7.784695709041462e-03
		d2 = 3.224671290700398e-01
		d3 = 2.445134137142996e+00
		d4 = 3.754408661907416e+00
	)
	const plow, phigh = 0.02425, 1 - 0.02425
	var x float64
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	case p <= phigh:
		q := p - 0.5
		r := q * q
		x = (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q /
			(((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
	// One Halley refinement for full machine precision.
	e := normCDF(x) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
	return x - u/(1+x*u/2)
}

// BlackScholes is the analytic European price used as the benchmark
// for the Monte Carlo engine.
func BlackScholes(payoff Payoff, s, k, t, r, q, sigma float64) float64 {
	if t <= 0 {
		if payoff == Call {
			return math.Max(s-k, 0)
		}
		return math.Max(k-s, 0)
	}
	if sigma == 0 {
		fwd := s * math.Exp((r-q)*t)
		disc := math.Exp(-r * t)
		if payoff == Call {
			return disc * math.Max(fwd-k, 0)
		}
		return disc * math.Max(k-fwd, 0)
	}
	sqrtT := math.Sqrt(t)
	d1 := (math.Log(s/k) + (r-q+0.5*sigma*sigma)*t) / (sigma * sqrtT)
	d2 := d1 - sigma*sqrtT
	if payoff == Call {
		return s*math.Exp(-q*t)*normCDF(d1) - k*math.Exp(-r*t)*normCDF(d2)
	}
	return k*math.Exp(-r*t)*normCDF(-d2) - s*math.Exp(-q*t)*normCDF(-d1)
}
