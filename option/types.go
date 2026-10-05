// Package option defines the product types, pricing parameters and the
// validation rules shared by every pricing entry point.
//
// Everything in this library is local: market parameters are supplied by the
// caller and random numbers are generated in-process. No external market data
// or remote service is ever contacted.
package option

import "time"

// Kind identifies the payoff family routed through the single pricing entry.
type Kind int

const (
	European Kind = iota + 1
	Asian
	Barrier
)

func (k Kind) String() string {
	switch k {
	case European:
		return "European"
	case Asian:
		return "Asian"
	case Barrier:
		return "Barrier"
	default:
		return "Unknown"
	}
}

// Right is a call or a put.
type Right int

const (
	Call Right = iota + 1
	Put
)

func (r Right) String() string {
	switch r {
	case Call:
		return "Call"
	case Put:
		return "Put"
	default:
		return "Unknown"
	}
}

// Monitoring describes how a barrier is observed.
type Monitoring int

const (
	// Continuous uses the Broadie-Glasserman-Kou Bernoulli correction so the
	// discrete simulation is an unbiased estimator of continuous monitoring.
	Continuous Monitoring = iota + 1
	// Discrete observes the barrier only at the simulation step grid points
	// (step 1 .. Steps; the final fixing at maturity is included).
	Discrete
)

func (m Monitoring) String() string {
	switch m {
	case Continuous:
		return "Continuous"
	case Discrete:
		return "Discrete"
	default:
		return "Unknown"
	}
}

// BarrierType is knock-in or knock-out.
type BarrierType int

const (
	KnockIn BarrierType = iota + 1
	KnockOut
)

func (b BarrierType) String() string {
	switch b {
	case KnockIn:
		return "KnockIn"
	case KnockOut:
		return "KnockOut"
	default:
		return "Unknown"
	}
}

// BarrierDirection is up or down relative to the initial spot.
type BarrierDirection int

const (
	Up BarrierDirection = iota + 1
	Down
)

func (d BarrierDirection) String() string {
	switch d {
	case Up:
		return "Up"
	case Down:
		return "Down"
	default:
		return "Unknown"
	}
}

// Params are the fully local inputs of one pricing request. All price levels
// (Spot, Strike, Barrier, Rebate) use the caller's currency unit; rates and
// Vol are in decimal (0.2 means 20%).
type Params struct {
	Kind   Kind
	Right  Right
	Spot   float64
	Strike float64
	// T is time to maturity expressed in years; it must be strictly positive.
	T     float64
	R     float64 // continuously compounded risk-free rate
	Q     float64 // continuous dividend / foreign rate yield
	Vol   float64 // volatility, must be non-negative
	Steps int     // time steps per simulated path

	// Barrier fields, used only when Kind == Barrier.
	Direction  BarrierDirection
	BarType    BarrierType
	Barrier    float64
	Monitoring Monitoring
	// Rebate is paid at knock-out (KnockOut) or at maturity if never knocked
	// in (KnockIn). Must be non-negative.
	Rebate float64
}

// Request is one job in a batch. Tag is echoed back unchanged in the result
// so callers can correlate outputs without relying on slice positions.
type Request struct {
	Tag    string
	Params Params
}

// Result is the outcome of a single pricing job.
type Result struct {
	Tag       string
	Price     float64
	StdErr    float64
	CI95Low   float64
	CI95High  float64
	Paths     int
	Steps     int
	Duration  time.Duration
	UsedCV    bool
	NonFinite int
	Converged bool
	Err       error
}

// Succeeded reports whether the job produced a finite, converged price.
func (r Result) Succeeded() bool { return r.Err == nil && r.Converged }
