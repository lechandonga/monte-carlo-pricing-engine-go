package mcengine

import "math"

// OptionType selects the payoff family. All types are priced through
// the same entry point, Engine.Price / Engine.PriceBatch.
type OptionType int

const (
	// European is a vanilla European call/put (analytic benchmark:
	// Black-Scholes).
	European OptionType = iota
	// Asian is an arithmetic-average Asian option with discrete
	// fixings at every simulation step.
	Asian
	// Barrier is a single-barrier knock-in / knock-out option.
	Barrier
)

func (t OptionType) String() string {
	switch t {
	case European:
		return "european"
	case Asian:
		return "asian"
	case Barrier:
		return "barrier"
	default:
		return "unknown"
	}
}

// Payoff is call or put.
type Payoff int

const (
	Call Payoff = iota
	Put
)

func (p Payoff) String() string {
	if p == Call {
		return "call"
	}
	return "put"
}

// BarrierDirection is the barrier position relative to the spot at
// inception.
type BarrierDirection int

const (
	// Up means the barrier sits above the spot price.
	Up BarrierDirection = iota
	// Down means the barrier sits below the spot price.
	Down
)

// BarrierKind is the knock semantics.
type BarrierKind int

const (
	// KnockOut: the option dies once the barrier is touched.
	KnockOut BarrierKind = iota
	// KnockIn: the option comes alive only if the barrier is touched.
	KnockIn
)

// Monitoring describes how barrier breaches are detected.
type Monitoring int

const (
	// MonitorDiscrete checks the barrier only at the NumSteps equally
	// spaced simulation dates. Fewer steps mean a "looser" barrier.
	MonitorDiscrete Monitoring = iota
	// MonitorContinuous treats the barrier as continuously observed;
	// between two simulated dates a Brownian-bridge crossing
	// probability is applied so results converge to the continuous
	// barrier price as steps are refined.
	MonitorContinuous
)

// BarrierSpec groups barrier-only fields.
type BarrierSpec struct {
	Level      float64
	Direction  BarrierDirection
	Kind       BarrierKind
	Monitoring Monitoring
}

// Option is a complete, self-contained pricing request.
type Option struct {
	Type     OptionType
	Payoff   Payoff
	Spot     float64 // S0, must be > 0
	Strike   float64 // K, must be > 0
	Maturity float64 // T in years, must be > 0
	Rate     float64 // continuously compounded risk-free rate
	DivYield float64 // continuously compounded dividend yield
	Vol      float64 // sigma, must be >= 0
	// Barrier is only used when Type == Barrier.
	Barrier BarrierSpec
}

// Validate enforces all parameter constraints. It returns an *Error
// with Kind == ErrInvalidParam and a field-specific message; it never
// silently repairs or defaults anything.
func (o *Option) Validate() error {
	for _, f := range []struct {
		name string
		v    float64
	}{
		{"Spot", o.Spot}, {"Strike", o.Strike}, {"Maturity", o.Maturity},
		{"Rate", o.Rate}, {"DivYield", o.DivYield}, {"Vol", o.Vol},
	} {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) {
			return invalidf("%s must be finite, got %v", f.name, f.v)
		}
	}
	if o.Spot <= 0 {
		return invalidf("Spot must be > 0, got %v", o.Spot)
	}
	if o.Strike <= 0 {
		return invalidf("Strike must be > 0, got %v", o.Strike)
	}
	if o.Maturity <= 0 {
		return invalidf("Maturity must be > 0, got %v", o.Maturity)
	}
	if o.Vol < 0 {
		return invalidf("Vol must be >= 0, got %v", o.Vol)
	}
	if o.Type != European && o.Type != Asian && o.Type != Barrier {
		return invalidf("unknown option type %d", int(o.Type))
	}
	if o.Type == Barrier {
		if err := o.validateBarrier(); err != nil {
			return err
		}
	}
	return nil
}

func (o *Option) validateBarrier() error {
	b := o.Barrier
	if math.IsNaN(b.Level) || math.IsInf(b.Level, 0) || b.Level <= 0 {
		return invalidf("Barrier.Level must be finite and > 0, got %v", b.Level)
	}
	switch b.Direction {
	case Up:
		if b.Level <= o.Spot {
			return invalidf("up barrier (%v) must be above spot (%v) at inception", b.Level, o.Spot)
		}
	case Down:
		if b.Level >= o.Spot {
			return invalidf("down barrier (%v) must be below spot (%v) at inception", b.Level, o.Spot)
		}
	default:
		return invalidf("unknown barrier direction %d", int(b.Direction))
	}
	if b.Kind != KnockOut && b.Kind != KnockIn {
		return invalidf("unknown barrier kind %d", int(b.Kind))
	}
	if b.Monitoring != MonitorDiscrete && b.Monitoring != MonitorContinuous {
		return invalidf("unknown monitoring %d", int(b.Monitoring))
	}
	// Contradictory barrier/strike combinations: the option can never
	// pay off, so pricing it would hide a specification mistake.
	if b.Kind == KnockOut && b.Direction == Up && o.Payoff == Call && b.Level <= o.Strike {
		return invalidf("up-and-out call with barrier (%v) <= strike (%v) can never pay off", b.Level, o.Strike)
	}
	if b.Kind == KnockOut && b.Direction == Down && o.Payoff == Put && b.Level >= o.Strike {
		return invalidf("down-and-out put with barrier (%v) >= strike (%v) can never pay off", b.Level, o.Strike)
	}
	return nil
}

// intrinsic computes the terminal vanilla payoff.
func (o *Option) intrinsic(s float64) float64 {
	if o.Payoff == Call {
		return math.Max(s-o.Strike, 0)
	}
	return math.Max(o.Strike-s, 0)
}
