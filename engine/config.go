package engine

import (
	"math"
	"runtime"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

// Config controls one Monte Carlo pricing run.
type Config struct {
	// Paths is the number of independent simulated trajectories. When
	// Antithetic is enabled it must be even and represents the number of
	// payoff samples (Paths/2 antithetic pairs).
	Paths int
	// Workers limits parallelism. Values <= 0 mean GOMAXPROCS; 1 forces
	// deterministic single-goroutine execution.
	Workers int
	// Seed seeds the deterministic PCG random stream. Two runs with equal
	// Seed + Params + Config produce bit-identical outputs regardless of the
	// number of workers.
	Seed uint64
	// Antithetic enables antithetic variates (paired +/- normal draws).
	Antithetic bool
	// ControlVariate enables an analytic control variate where one exists
	// (geometric average for arithmetic Asians).
	ControlVariate bool
	// MinPaths is the minimum number of effective finite samples required.
	// Zero defaults to 1024.
	MinPaths int
	// RelTol is the optional convergence criterion: the 95% confidence
	// interval half-width must be <= RelTol * |Price|. Zero disables the
	// tolerance check; negative values are a configuration error.
	RelTol float64
	// BatchSize is the number of paths accumulated per chunk; zero defaults
	// to a size that yields roughly 16 chunks per worker.
	BatchSize int
}

func (c Config) workers() int {
	if c.Workers <= 0 {
		return runtime.GOMAXPROCS(0)
	}
	return c.Workers
}

func (c Config) minPaths() int {
	if c.MinPaths <= 0 {
		return 1024
	}
	return c.MinPaths
}

func (c Config) validate(p option.Params) error {
	if c.Paths <= 0 {
		return mkErr(option.ErrInvalidSimConfig, "paths must be positive, got %d", c.Paths)
	}
	if c.Antithetic && c.Paths%2 != 0 {
		return mkErr(option.ErrInvalidSimConfig, "antithetic sampling requires an even path count, got %d", c.Paths)
	}
	if c.Paths < c.minPaths() {
		return mkErr(option.ErrTooFewPaths, "requested paths %d below required minimum %d", c.Paths, c.minPaths())
	}
	if math.IsNaN(c.RelTol) || math.IsInf(c.RelTol, 0) || c.RelTol < 0 {
		return mkErr(option.ErrInvalidSimConfig, "relative tolerance must be finite and non-negative, got %v", c.RelTol)
	}
	if c.Workers < 0 {
		return mkErr(option.ErrInvalidSimConfig, "workers must be >= 0, got %d", c.Workers)
	}
	if c.BatchSize < 0 {
		return mkErr(option.ErrInvalidSimConfig, "batch size must be >= 0, got %d", c.BatchSize)
	}
	return p.Validate()
}

// GreekConfig controls numerical sensitivity estimation.
type GreekConfig struct {
	Sim Config
	// Bump sizes; zeros select defaults: spot/strike 1%, vol 1 vol point,
	// rate 1bp, maturity 1 day.
	BumpSpot, BumpVol, BumpRate, BumpTime float64
}

// Greeks are Monte Carlo sensitivities, each carrying its own standard error.
type Greeks struct {
	Delta, DeltaSE float64
	Gamma, GammaSE float64
	Vega, VegaSE   float64
	Rho, RhoSE     float64
	Theta, ThetaSE float64
}
