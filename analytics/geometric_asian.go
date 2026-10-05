package analytics

import "math"

// GeometricAsian gives the closed-form price of an arithmetic-grid discretely
// monitored geometric-average Asian option (monitoring at steps 1..n; the
// common convention used by the Monte Carlo engine).
//
// Under GBM the geometric average is lognormal. For averaging over fixings
// t_i = i*t/n, i = 1..n, with log S_t_i ~ N(mu_i, sigma_i^2):
//
//	E[(1/n) sum ln S_i] = ln S + (nu/2n) sum (2i-1)
//	                       where nu = r - q - vol^2/2
//	Var = (vol^2/n^2) sum_i (2i-1)^2 ... (see derivation inside)
func GeometricAsian(spot, strike, t, r, q, vol float64, n int, call bool) float64 {
	if vol == 0 {
		forward := spot * math.Exp((r-q)*t*(float64(n)+1)/(2*float64(n)))
		d := math.Exp(-r * t)
		if call {
			return d * math.Max(forward-strike, 0)
		}
		return d * math.Max(strike-forward, 0)
	}

	nf := float64(n)
	nu := r - q - vol*vol/2

	// Mean of (1/n) * sum_i ln S(t_i), minus ln spot.
	// sum_{i=1..n} (2i-1) = n^2, so muAvg = nu*t*(n+1)/(2n).
	muAvg := nu * t * (nf + 1) / (2 * nf)

	// Variance of the average: (vol^2 / n^2) * sum_i (2i-1) * t_i? Derive:
	// Cov(ln S_i, ln S_j) = vol^2 * min(t_i,t_j); with t_i = i*t/n:
	// Var = vol^2 * t / n^3 * sum_{i,j} min(i,j), where
	// sum_{i,j} min(i,j) = sum_{m=1..n} m^2 = n(n+1)(2n+1)/6.
	sumMin := nf * (nf + 1) * (2*nf + 1) / 6
	sigAvgSq := vol * vol * t * sumMin / (nf * nf * nf)
	sigAvg := math.Sqrt(sigAvgSq)

	d1 := (math.Log(spot/strike) + muAvg + sigAvgSq) / sigAvg
	d2 := d1 - sigAvg
	discount := math.Exp(-r * t)
	growth := math.Exp(muAvg + sigAvgSq/2)
	if call {
		return discount * (spot*growth*Phi(d1) - strike*Phi(d2))
	}
	return discount * (strike*Phi(-d2) - spot*growth*Phi(-d1))
}
