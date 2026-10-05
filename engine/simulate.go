package engine

import (
	"math"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/analytics"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

// z95 is the 97.5% standard normal quantile, used for 95% confidence
// intervals of the Monte Carlo mean.
const z95 = 1.9599639845400545

type batchStats struct {
	sum       float64
	sumSq     float64
	pairSum   float64
	pairSumSq float64
	pairs     int
	nonFinite int
	count     int
}

// simulateBatches runs paths [start,end) and returns the partial statistics.
// The result depends only on (cfg, p, start, end), never on scheduling, so
// chunks can run on any goroutine and be merged in index order.
func simulateBatch(p option.Params, cfg Config, start, end int) batchStats {
	st := batchStats{}
	dt := p.T / float64(p.Steps)
	drift := (p.R - p.Q - p.Vol*p.Vol/2) * dt
	diff := p.Vol * math.Sqrt(dt)
	discount := math.Exp(-p.R * p.T)

	useCV := cfg.ControlVariate && p.Kind == option.Asian
	// Precomputed analytic geometric-Asian mean for the control variate.
	var cvMean float64
	if useCV {
		// simulatePath discounts the final payoff itself, so the control
		// mean must be the *undiscounted* geometric-Asian price.
		cvMean = analytics.GeometricAsian(p.Spot, p.Strike, p.T, p.R, p.Q, p.Vol, p.Steps, p.Right == option.Call) / discount
	}

	step := 1
	if cfg.Antithetic {
		step = 2
	}
	for i := start; i < end; i += step {
		var samples [2]float64
		if cfg.Antithetic {
			// One RNG stream per pair. All randomness (normals AND the
			// Bernoulli-bridging uniforms used for continuous barriers) is
			// drawn up-front into shared slices, so both legs consume the
			// identical stream in the identical order. Generating uniforms
			// inside path simulation would desynchronise the two legs.
			rng := streamFor(cfg.Seed, i/2)
			zs := make([]float64, p.Steps)
			for k := range zs {
				zs[k] = rng.NormFloat64()
			}
			var us []float64
			if p.Kind == option.Barrier && p.Monitoring == option.Continuous {
				us = make([]float64, p.Steps)
				for k := range us {
					us[k] = rng.Float64()
				}
			}
			samples[0] = simulatePath(p, zs, us, +1, drift, diff, dt, discount, useCV, cvMean)
			samples[1] = simulatePath(p, zs, us, -1, drift, diff, dt, discount, useCV, cvMean)
		} else {
			rng := streamFor(cfg.Seed, i)
			zs := make([]float64, p.Steps)
			for k := range zs {
				zs[k] = rng.NormFloat64()
			}
			var us []float64
			if p.Kind == option.Barrier && p.Monitoring == option.Continuous {
				us = make([]float64, p.Steps)
				for k := range us {
					us[k] = rng.Float64()
				}
			}
			samples[0] = simulatePath(p, zs, us, +1, drift, diff, dt, discount, useCV, cvMean)
		}
		n := 1
		if cfg.Antithetic {
			n = 2
		}
		if cfg.Antithetic {
			v0, v1 := samples[0], samples[1]
			if !finiteV(v0) || !finiteV(v1) {
				if !finiteV(v0) {
					st.nonFinite++
				}
				if !finiteV(v1) {
					st.nonFinite++
				}
				continue
			}
			// The antithetic ESTIMATOR is the per-pair mean. Its mean and
			// variance must be computed over pairs: treating the two
			// negatively correlated legs as independent samples would hide
			// the variance reduction and bias the standard error.
			pm := 0.5 * (v0 + v1)
			st.pairSum += pm
			st.pairSumSq += pm * pm
			st.pairs++
			st.sum += v0 + v1
			st.sumSq += v0*v0 + v1*v1
			st.count += 2
			continue
		}
		for k := 0; k < n; k++ {
			v := samples[k]
			if !finiteV(v) {
				st.nonFinite++
				continue
			}
			st.sum += v
			st.sumSq += v * v
			st.count++
		}
	}
	return st
}

func finiteV(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// simulatePath simulates one GBM trajectory. sign flips every normal draw
// (antithetic leg). The same RNG stream is shared by a pair so the two legs
// consume identical |z| values.
func simulatePath(p option.Params, zs, us []float64, sign, drift, diff, dt, discount float64, useCV bool, cvMean float64) float64 {
	s := p.Spot
	var arithSum, geoLogSum float64
	hit := false
	// For continuous monitoring we carry the previous step price.
	prev := p.Spot

	for step := 1; step <= p.Steps; step++ {
		z := sign * zs[step-1]
		s *= math.Exp(drift + diff*z)

		if p.Kind == option.Asian {
			arithSum += s
			geoLogSum += math.Log(s)
		}

		if p.Kind == option.Barrier {
			switch p.Monitoring {
			case option.Discrete:
				if crossed(p, s) {
					hit = true
				}
			case option.Continuous:
				// Broadie-Glasserman-Kou Bernoulli bridging: conditional on
				// (prev, s), the probability that the continuous GBM path
				// touched the barrier is exp(-2 ln(H/prev) ln(H/s)/sig^2 dt).
				if crossed(p, s) {
					hit = true
				} else {
					var prob float64
					if p.Direction == option.Up {
						if prev < p.Barrier && s < p.Barrier {
							prob = math.Exp(-2 * math.Log(p.Barrier/prev) * math.Log(p.Barrier/s) / (p.Vol * p.Vol * dt))
						}
					} else {
						if prev > p.Barrier && s > p.Barrier {
							prob = math.Exp(-2 * math.Log(prev/p.Barrier) * math.Log(s/p.Barrier) / (p.Vol * p.Vol * dt))
						}
					}
					if prob > 0 && us[step-1] < prob {
						hit = true
					}
				}
			}
			prev = s
		}
	}

	var payoff float64
	intrinsic := func(level float64) float64 {
		if p.Right == option.Call {
			return math.Max(level-p.Strike, 0)
		}
		return math.Max(p.Strike-level, 0)
	}

	switch p.Kind {
	case option.European:
		payoff = intrinsic(s)
	case option.Asian:
		avg := arithSum / float64(p.Steps)
		payoff = intrinsic(avg)
		if useCV {
			geoAvg := math.Exp(geoLogSum / float64(p.Steps))
			cvPayoff := intrinsic(geoAvg)
			// The adjusted payoff Y + (E[X] - X) keeps the expectation of Y
			// while removing the control variate's variance.
			payoff = payoff + (cvMean - cvPayoff)
		}
	case option.Barrier:
		switch p.BarType {
		case option.KnockOut:
			if hit {
				payoff = p.Rebate
			} else {
				payoff = intrinsic(s)
			}
		case option.KnockIn:
			if hit {
				payoff = intrinsic(s)
			} else {
				payoff = p.Rebate
			}
		}
	}
	return discount * payoff
}

func crossed(p option.Params, s float64) bool {
	if p.Direction == option.Up {
		return s >= p.Barrier
	}
	return s <= p.Barrier
}

// aggregate merges per-chunk statistics in deterministic index order and
// computes price, standard error and the 95% interval. It enforces the
// error contracts: too few finite samples, non-finite statistics and failed
// convergence tolerance are reported, never silently returned.
// reducePairwise sums chunk totals through a fixed binary tree. The same
// chunks are produced for every worker count, and the tree evaluation order
// is fixed, so multi-worker runs are bit-identical to single-worker runs
// (naive sequential += would differ by rounding at chunk boundaries).
func reducePairwise(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	buf := append([]float64(nil), vals...)
	for len(buf) > 1 {
		nxt := make([]float64, (len(buf)+1)/2)
		for i := 0; i < len(buf); i += 2 {
			if i+1 < len(buf) {
				nxt[i/2] = buf[i] + buf[i+1]
			} else {
				nxt[i/2] = buf[i]
			}
		}
		buf = nxt
	}
	return buf[0]
}

func aggregate(chunks []batchStats, cfg Config) (price, stderr, lo, hi float64, nonFinite, n int, err error) {
	sums := make([]float64, len(chunks))
	sumsq := make([]float64, len(chunks))
	pairs := make([]float64, len(chunks))
	pairsq := make([]float64, len(chunks))
	nPairs := 0
	for i, c := range chunks {
		sums[i] = c.sum
		sumsq[i] = c.sumSq
		pairs[i] = c.pairSum
		pairsq[i] = c.pairSumSq
		nPairs += c.pairs
		nonFinite += c.nonFinite
		n += c.count
	}
	price = reducePairwise(sums)
	if cfg.Antithetic {
		// Effective sample size is the number of complete pairs.
		n = nPairs
		if nPairs < cfg.minPaths()/2 {
			return 0, 0, 0, 0, nonFinite, 0,
				mkErr(option.ErrTooFewPaths, "only %d finite antithetic pairs (minimum %d); non-finite legs: %d",
					nPairs, cfg.minPaths()/2, nonFinite)
		}
		ps := reducePairwise(pairs)
		psq := reducePairwise(pairsq)
		mean := ps / float64(nPairs)
		variance := (psq/float64(nPairs) - mean*mean) * float64(nPairs) / float64(nPairs-1)
		if variance < 0 {
			variance = 0
		}
		return finalize(mean, math.Sqrt(variance/float64(nPairs)), nonFinite, nPairs, cfg)
	}
	if n < cfg.minPaths() {
		return 0, 0, 0, 0, nonFinite, n,
			mkErr(option.ErrTooFewPaths, "only %d finite payoff samples (minimum %d); non-finite paths: %d",
				n, cfg.minPaths(), nonFinite)
	}
	sumSq := reducePairwise(sumsq)
	mean := price / float64(n)
	variance := (sumSq/float64(n) - mean*mean) * float64(n) / float64(n-1)
	if variance < 0 {
		variance = 0 // round-off guard for a zero-variance estimator
	}
	stderr = math.Sqrt(variance / float64(n))
	return finalize(mean, stderr, nonFinite, n, cfg)
}

func finalize(mean, stderr float64, nonFinite, n int, cfg Config) (price, se, lo, hi float64, nf, count int, err error) {
	half := z95 * stderr
	lo, hi = mean-half, mean+half
	if math.IsNaN(mean) || math.IsInf(mean, 0) || math.IsNaN(stderr) || math.IsInf(stderr, 0) {
		return mean, stderr, lo, hi, nonFinite, n,
			mkErr(option.ErrNonFiniteResult, "price=%v stderr=%v", mean, stderr)
	}
	if cfg.RelTol > 0 {
		scale := math.Max(math.Abs(mean), 1e-12)
		if half/scale > cfg.RelTol {
			return mean, stderr, lo, hi, nonFinite, n,
				mkErr(option.ErrNotConverged,
					"95%% CI half-width %.6g exceeds relative tolerance %.6g of price %.6g", half, cfg.RelTol, mean)
		}
	}
	return mean, stderr, lo, hi, nonFinite, n, nil
}
