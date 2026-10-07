package mcengine

import "math"

// simPlan describes the merged simulation grid: the union of the
// equally spaced numerical step grid and (when given) the explicit
// observation calendar. Observations (Asian fixings, discrete barrier
// checks, discrete lookback extrema) are taken ONLY at obs nodes; the
// step density only controls numerical accuracy.
type simPlan struct {
	times []float64 // strictly increasing node times, starting > 0, ending at T
	obs   []bool    // obs[k]: node k is an observation date
	nObs  int       // number of observation dates
}

// buildPlan constructs the merged grid.
//
// A nil calendar reproduces the legacy semantics: step-end nodes are
// all observation dates. With an explicit calendar, only calendar
// dates are observation dates; the calendar's final date is T.
func buildPlan(maturity float64, steps int, calendar []float64) simPlan {
	nodes := make([]float64, 0, steps+len(calendar))
	isObs := make([]bool, 0, steps+len(calendar))

	appendNode := func(t float64, obs bool) {
		if len(nodes) > 0 && nodes[len(nodes)-1] == t {
			if obs {
				isObs[len(isObs)-1] = true
			}
			return
		}
		nodes = append(nodes, t)
		isObs = append(isObs, obs)
	}

	stepEnds := make([]float64, steps)
	for j := 1; j <= steps; j++ {
		stepEnds[j-1] = maturity * float64(j) / float64(steps)
	}

	legacy := calendar == nil
	si, ci := 0, 0
	for {
		ts := math.Inf(1)
		if si < len(stepEnds) {
			ts = stepEnds[si]
		}
		var tc float64
		hasC := false
		if !legacy && ci < len(calendar) {
			tc, hasC = calendar[ci], true
		}
		if math.IsInf(ts, 1) && !hasC {
			break
		}
		switch {
		case hasC && tc < ts:
			appendNode(tc, true)
			ci++
		case ts < tc || (!hasC):
			appendNode(ts, legacy)
			si++
		default: // equal step end and calendar date
			appendNode(ts, true)
			si++
			ci++
		}
	}

	nObs := 0
	for _, b := range isObs {
		if b {
			nObs++
		}
	}
	return simPlan{times: nodes, obs: isObs, nObs: nObs}
}

// pathResult carries everything a single simulated path contributes.
type pathResult struct {
	terminal float64
	sumObs   float64 // sum of spot at observation dates
	nObs     int
	breached bool
	minObs   float64 // extrema at observation dates
	maxObs   float64
	minCont  float64 // extrema over continuous monitoring
	maxCont  float64
}

// simulate evolves one GBM path. zs has one standard normal per grid
// segment and us one uniform per segment (bridge sampling uses us;
// passing nil falls back to a deterministic endpoint hash, retained
// only so callers can never hit a nil-map). sign flips the Brownian
// increments for antithetic sampling while bridge uniforms are shared
// (common random numbers).
func simulate(opt *Option, plan simPlan, zs, us []float64, sign float64) (pathResult, error) {
	drift := opt.Rate - opt.DivYield - 0.5*opt.Vol*opt.Vol

	res := pathResult{
		nObs:    0,
		minObs:  math.Inf(1),
		maxObs:  math.Inf(-1),
		minCont: opt.Spot,
		maxCont: opt.Spot,
	}

	contBarrier := opt.Type == Barrier && opt.Barrier.Monitoring == MonitorContinuous
	contLookback := opt.Type == Lookback && opt.Lookback.Monitoring == MonitorContinuous

	prevT := 0.0
	prevLog := math.Log(opt.Spot)
	spot := opt.Spot

	for k, t := range plan.times {
		dt := t - prevT
		logNext := prevLog + drift*dt + opt.Vol*math.Sqrt(dt)*sign*zs[k]
		next := math.Exp(logNext)
		if math.IsInf(next, 0) || math.IsNaN(next) {
			return res, &Error{Kind: ErrNonFinite, Detail: "spot path became non-finite"}
		}

		if contBarrier {
			if barrierBridge(opt, spot, next, dt, bridgeUniform(us, k, prevLog, logNext)) {
				res.breached = true
			}
		}
		if contLookback {
			hi, lo := bridgeExtrema(spot, next, prevLog, logNext, dt, opt.Vol, bridgeUniform(us, k, prevLog, logNext))
			if hi > res.maxCont {
				res.maxCont = hi
			}
			if lo < res.minCont {
				res.minCont = lo
			}
		}

		if plan.obs[k] {
			res.nObs++
			res.sumObs += next
			if next < res.minObs {
				res.minObs = next
			}
			if next > res.maxObs {
				res.maxObs = next
			}
			if opt.Type == Barrier && opt.Barrier.Monitoring == MonitorDiscrete {
				b := opt.Barrier
				if b.Direction == Up && next >= b.Level || b.Direction == Down && next <= b.Level {
					res.breached = true
				}
			}
		}

		spot, prevLog, prevT = next, logNext, t
	}

	res.terminal = spot
	return res, nil
}

func bridgeUniform(us []float64, k int, x1, x2 float64) float64 {
	if us != nil {
		return us[k]
	}
	return deterministicUniform(x1, x2)
}

// barrierBridge reports whether the log-Brownian bridge between
// (sPrev -> sNext) crosses the barrier, using the exact conditional
// crossing probability (Reiner's Brownian-bridge correction).
func barrierBridge(opt *Option, sPrev, sNext, dt, u float64) bool {
	b := opt.Barrier
	if b.Direction == Up && (sPrev >= b.Level || sNext >= b.Level) {
		return true
	}
	if b.Direction == Down && (sPrev <= b.Level || sNext <= b.Level) {
		return true
	}
	if opt.Vol == 0 || dt <= 0 {
		return false
	}
	x1, x2, h := math.Log(sPrev), math.Log(sNext), math.Log(b.Level)
	pCross := math.Exp(-2 * (h - x1) * (h - x2) / (opt.Vol * opt.Vol * dt))
	return u < pCross
}

// bridgeExtrema returns the sampled (max, min) of the underlying over
// one segment conditional on its endpoints. One independent uniform
// per segment draws the running extremum from its exact conditional
// law, so extrema converge to continuous-monitoring values as the
// grid is refined, independent of endpoint spacing.
func bridgeExtrema(sPrev, sNext, x1, x2, dt, sigma, u float64) (hi, lo float64) {
	hi = math.Max(sPrev, sNext)
	lo = math.Min(sPrev, sNext)
	if sigma == 0 || dt <= 0 {
		return hi, lo
	}
	v := sigma * sigma * dt
	// Conditional maximum: P(M <= m | x1,x2) =
	// 1 - exp(-2(m-x1)(m-x2)/v), m >= max(x1,x2).
	d := math.Sqrt((x2-x1)*(x2-x1) - v*math.Log(1-u))
	mMax := 0.5*(x1+x2) + d
	// Conditional minimum by symmetry on the same uniform draw.
	mMin := 0.5*(x1+x2) - d
	if e := math.Exp(mMax); e > hi {
		hi = e
	}
	if e := math.Exp(mMin); e < lo {
		lo = e
	}
	return hi, lo
}

// payoff computes the undiscounted payoff from a simulated path.
func payoff(opt *Option, res pathResult) float64 {
	switch opt.Type {
	case European:
		return opt.intrinsic(res.terminal)
	case Asian:
		avg := res.sumObs / float64(res.nObs)
		return opt.intrinsic(avg)
	case Barrier:
		alive := opt.Barrier.Kind == KnockOut && !res.breached ||
			opt.Barrier.Kind == KnockIn && res.breached
		if alive {
			return opt.intrinsic(res.terminal)
		}
		return 0
	case Lookback:
		lb := opt.Lookback
		cont := lb.Monitoring == MonitorContinuous
		switch lb.StrikeKind {
		case FloatingStrike:
			if opt.Payoff == Call {
				mn := res.minObs
				if cont {
					mn = res.minCont
				}
				return math.Max(res.terminal-mn, 0)
			}
			mx := res.maxObs
			if cont {
				mx = res.maxCont
			}
			return math.Max(mx-res.terminal, 0)
		case FixedStrike:
			if opt.Payoff == Call {
				mx := res.maxObs
				if cont {
					mx = res.maxCont
				}
				return math.Max(mx-opt.Strike, 0)
			}
			mn := res.minObs
			if cont {
				mn = res.minCont
			}
			return math.Max(opt.Strike-mn, 0)
		}
	}
	return 0
}
