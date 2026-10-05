package option

import "math"

// Validate checks every business rule on the request parameters. It returns
// an error carrying a distinct ErrorCode; it never repairs the input or
// returns a default value.
func (p Params) Validate() error {
	switch p.Kind {
	case European, Asian, Barrier:
	default:
		return fail(ErrInvalidParams, "unknown option kind %d", p.Kind)
	}
	switch p.Right {
	case Call, Put:
	default:
		return fail(ErrInvalidParams, "unknown option right %d", p.Right)
	}
	if !finitePositive(p.Spot) {
		return fail(ErrInvalidParams, "spot must be finite and positive, got %v", p.Spot)
	}
	if !finitePositive(p.Strike) {
		return fail(ErrInvalidParams, "strike must be finite and positive, got %v", p.Strike)
	}
	if math.IsNaN(p.T) || math.IsInf(p.T, 0) || p.T <= 0 {
		return fail(ErrNonPositiveMaturity, "time to maturity must be finite and strictly positive, got %v", p.T)
	}
	if math.IsNaN(p.R) || math.IsInf(p.R, 0) {
		return fail(ErrInvalidParams, "risk-free rate must be finite, got %v", p.R)
	}
	if math.IsNaN(p.Q) || math.IsInf(p.Q, 0) {
		return fail(ErrInvalidParams, "dividend yield must be finite, got %v", p.Q)
	}
	if math.IsNaN(p.Vol) || math.IsInf(p.Vol, 0) || p.Vol < 0 {
		return fail(ErrNegativeVol, "volatility must be finite and non-negative, got %v", p.Vol)
	}
	if p.Steps <= 0 {
		return fail(ErrInvalidSteps, "simulation steps must be a positive integer, got %d", p.Steps)
	}

	if p.Kind == Barrier {
		if err := p.validateBarrier(); err != nil {
			return err
		}
	}
	if math.IsNaN(p.Rebate) || math.IsInf(p.Rebate, 0) || p.Rebate < 0 {
		return fail(ErrInvalidParams, "rebate must be finite and non-negative, got %v", p.Rebate)
	}
	return nil
}

func (p Params) validateBarrier() error {
	switch p.Direction {
	case Up, Down:
	default:
		return fail(ErrBarrierConfig, "barrier direction must be Up or Down, got %d", p.Direction)
	}
	switch p.BarType {
	case KnockIn, KnockOut:
	default:
		return fail(ErrBarrierConfig, "barrier type must be KnockIn or KnockOut, got %d", p.BarType)
	}
	switch p.Monitoring {
	case Continuous, Discrete:
	default:
		return fail(ErrBarrierConfig, "barrier monitoring must be Continuous or Discrete, got %d", p.Monitoring)
	}
	if !finitePositive(p.Barrier) {
		return fail(ErrBarrierConfig, "barrier level must be finite and positive, got %v", p.Barrier)
	}
	// The barrier is defined relative to the initial spot. An up barrier must
	// start out-of-reach above spot; a down barrier below spot. A barrier
	// exactly at spot would trigger ambiguously at t=0 and is rejected.
	switch p.Direction {
	case Up:
		if p.Barrier <= p.Spot {
			return fail(ErrBarrierConfig, "up-barrier %v must be strictly above spot %v", p.Barrier, p.Spot)
		}
	case Down:
		if p.Barrier >= p.Spot {
			return fail(ErrBarrierConfig, "down-barrier %v must be strictly below spot %v", p.Barrier, p.Spot)
		}
	}
	// The barrier must not contradict the strike: a knock-out option that is
	// already beyond the barrier at maturity would have a degenerate payoff.
	// For an up barrier the barrier must be above an out-call strike (otherwise
	// the call is always extinguished when in the money); symmetrically for a
	// down barrier and an out-put.
	if p.BarType == KnockOut {
		switch {
		case p.Direction == Up && p.Right == Call && p.Barrier <= p.Strike:
			return fail(ErrBarrierConfig,
				"up-and-out call is degenerate: barrier %v must be above strike %v", p.Barrier, p.Strike)
		case p.Direction == Down && p.Right == Put && p.Barrier >= p.Strike:
			return fail(ErrBarrierConfig,
				"down-and-out put is degenerate: barrier %v must be below strike %v", p.Barrier, p.Strike)
		}
	}
	if p.BarType == KnockIn {
		switch {
		case p.Direction == Up && p.Right == Call && p.Barrier <= p.Strike:
			// Up-and-in call with barrier below strike: payoff always knocks in
			// before the call can be in the money, but it is a valid contract
			// (equivalent to a vanilla with a trigger); allowed.
		case p.Direction == Down && p.Right == Put && p.Barrier >= p.Strike:
			// Symmetric valid case.
		}
	}
	return nil
}

func finitePositive(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0) && x > 0
}
