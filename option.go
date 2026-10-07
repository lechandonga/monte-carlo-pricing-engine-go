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
	// Lookback is a lookback option with floating or fixed strike.
	Lookback
)

func (t OptionType) String() string {
	switch t {
	case European:
		return "european"
	case Asian:
		return "asian"
	case Barrier:
		return "barrier"
	case Lookback:
		return "lookback"
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

// LookbackKind selects floating- or fixed-strike lookback semantics.
type LookbackKind int

const (
	// FloatingStrike lookback: payoff S_T - min S (call) or max S - S_T (put).
	FloatingStrike LookbackKind = iota
	// FixedStrike lookback: payoff max S - K (call) or K - min S (put).
	FixedStrike
)

func (k LookbackKind) String() string {
	if k == FloatingStrike {
		return "floating-strike"
	}
	return "fixed-strike"
}

// LookbackSpec groups lookback-only fields. Extremum accumulation
// always starts from Spot at time 0.
type LookbackSpec struct {
	Kind       LookbackKind
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
	// Lookback is only used when Type == Lookback.
	Lookback LookbackSpec
	// ObservationDates is the strictly increasing observation
	// calendar in years. It is used by Asian fixings, discrete
	// barrier monitoring and discrete lookback extremum updates. The
	// calendar must lie in [0, Maturity], be strictly increasing and
	// end exactly at Maturity; it is never silently sorted,
	// de-duplicated or rounded. Nil selects the legacy equally spaced
	// calendar (0..T for Asian, dt..T for barrier/lookback). Ignored
	// by European and by continuously monitored products, where the
	// simulation grid only affects numerical accuracy.
	ObservationDates []float64
}

// ValidateCalendar checks an observation calendar against maturity.
// Every failure class gets its own message: empty, non-finite, out of
// range, duplicate or out of order.
func ValidateCalendar(dates []float64, maturity float64) error {
	if len(dates) == 0 {
		return invalidf("observation calendar must contain at least one date")
	}
	prev := 0.0
	for i, d := range dates {
		if math.IsNaN(d) || math.IsInf(d, 0) {
			return invalidf("observation date #%d is not finite: %v", i, d)
		}
		if d < 0 {
			return invalidf("observation date #%d (%v) is before time 0", i, d)
		}
		if d > maturity {
			return invalidf("observation date #%d (%v) is after maturity (%v)", i, d, maturity)
		}
		if i > 0 && d <= prev {
			if d == prev {
				return invalidf("observation date #%d (%v) duplicates date #%d", i, d, i-1)
			}
			return invalidf("observation date #%d (%v) is not strictly increasing (previous %v)", i, d, prev)
		}
		prev = d
	}
	if dates[len(dates)-1] != maturity {
		return invalidf("observation calendar must end at maturity (%v), last date is %v",
			maturity, dates[len(dates)-1])
	}
	return nil
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
	if o.Type != European && o.Type != Asian && o.Type != Barrier && o.Type != Lookback {
		return invalidf("unknown option type %d", int(o.Type))
	}
	if o.Type == Barrier {
		if err := o.validateBarrier(); err != nil {
			return err
		}
	}
	if o.Type == Lookback {
		if err := o.validateLookback(); err != nil {
			return err
		}
	}
	if o.ObservationDates != nil && o.Type != European {
		if len(o.ObservationDates) == 0 {
			return invalidf("observation calendar must contain at least one date")
		}
		if err := ValidateCalendar(o.ObservationDates, o.Maturity); err != nil {
			return err
		}
	}
	return nil
}

func (o *Option) validateLookback() error {
	l := o.Lookback
	if l.Kind != FloatingStrike && l.Kind != FixedStrike {
		return invalidf("unknown lookback kind %d", int(l.Kind))
	}
	if l.Monitoring != MonitorDiscrete && l.Monitoring != MonitorContinuous {
		return invalidf("unknown monitoring %d", int(l.Monitoring))
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
