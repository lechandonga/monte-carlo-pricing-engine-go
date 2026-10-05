package mcengine

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
)

// DefaultMinPaths is the floor on NumPaths when Config.MinPaths is 0.
const DefaultMinPaths = 1000

// Config controls one pricing run. A zero Config is invalid; at
// minimum NumPaths, NumSteps and Seed should be set (Seed may be 0,
// which is a perfectly valid seed).
type Config struct {
	// NumPaths is the number of simulated paths. With Antithetic on,
	// this is the number of antithetic pairs.
	NumPaths int
	// NumSteps is the number of time steps per path (also the number
	// of Asian fixings / discrete barrier monitoring dates).
	NumSteps int
	// Seed is the base random seed. Results depend only on Seed and
	// the inputs, never on scheduling or worker count.
	Seed uint64
	// Antithetic enables antithetic-variate variance reduction.
	Antithetic bool
	// MinPaths overrides DefaultMinPaths; runs below it fail with
	// ErrTooFewPaths.
	MinPaths int
	// RelTol, when > 0, requires StdErr/|Price| <= RelTol, otherwise
	// the run fails with ErrNotConverged.
	RelTol float64
	// MaxWorkers caps parallelism for PriceBatch. 0 means NumCPU.
	MaxWorkers int
}

func (c *Config) minPaths() int {
	if c.MinPaths > 0 {
		return c.MinPaths
	}
	return DefaultMinPaths
}

func (c *Config) validate() error {
	if c.NumPaths < c.minPaths() {
		return &Error{Kind: ErrTooFewPaths}
	}
	if c.NumSteps <= 0 {
		return invalidf("NumSteps must be > 0, got %d", c.NumSteps)
	}
	if c.RelTol < 0 {
		return invalidf("RelTol must be >= 0, got %v", c.RelTol)
	}
	return nil
}

// Result is the output of a single pricing run.
type Result struct {
	Price      float64
	StdErr     float64
	CILow      float64 // 95% confidence interval
	CIHigh     float64
	Paths      int
	Steps      int
	Antithetic bool
}

// Engine is stateless and safe for concurrent use.
type Engine struct{}

// NewEngine returns a ready-to-use engine.
func NewEngine() *Engine { return &Engine{} }

// splitmix64 derives independent, deterministic sub-seeds.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// Price prices one option. It is fully deterministic for a given
// (Option, Config) pair regardless of goroutine scheduling.
func (e *Engine) Price(opt Option, cfg Config) (Result, error) {
	if err := opt.Validate(); err != nil {
		return Result{}, err
	}
	if err := cfg.validate(); err != nil {
		return Result{}, err
	}
	rng := rand.New(rand.NewPCG(cfg.Seed, splitmix64(cfg.Seed)))

	n := cfg.NumPaths
	payoffs := make([]float64, 0, n)
	if cfg.Antithetic {
		payoffs = make([]float64, 0, 2*n)
	}
	for i := 0; i < n; i++ {
		zs := make([]float64, cfg.NumSteps)
		for j := range zs {
			zs[j] = normInv(rng.Float64())
		}
		p, err := simulatePath(&opt, &cfg, zs, 1)
		if err != nil {
			return Result{}, err
		}
		payoffs = append(payoffs, p)
		if cfg.Antithetic {
			pa, err := simulatePath(&opt, &cfg, zs, -1)
			if err != nil {
				return Result{}, err
			}
			payoffs = append(payoffs, pa)
		}
	}

	m := len(payoffs)
	var sum, sumSq float64
	for _, p := range payoffs {
		sum += p
		sumSq += p * p
	}
	mean := sum / float64(m)
	variance := (sumSq - sum*sum/float64(m)) / float64(m-1)
	if variance < 0 {
		variance = 0
	}
	stdErr := math.Sqrt(variance / float64(m))
	if math.IsNaN(mean) || math.IsInf(mean, 0) || math.IsNaN(stdErr) || math.IsInf(stdErr, 0) {
		return Result{}, &Error{Kind: ErrNonFinite, Detail: "price or standard error is not finite"}
	}
	if cfg.RelTol > 0 && math.Abs(mean) > 0 && stdErr/math.Abs(mean) > cfg.RelTol {
		return Result{}, &Error{Kind: ErrNotConverged}
	}
	return Result{
		Price:      mean,
		StdErr:     stdErr,
		CILow:      mean - 1.96*stdErr,
		CIHigh:     mean + 1.96*stdErr,
		Paths:      m,
		Steps:      cfg.NumSteps,
		Antithetic: cfg.Antithetic,
	}, nil
}

// simulatePath evolves one GBM path with the given standard normals
// (multiplied by sign, used for antithetic sampling) and returns the
// discounted payoff.
func simulatePath(opt *Option, cfg *Config, zs []float64, sign float64) (float64, error) {
	steps := cfg.NumSteps
	dt := opt.Maturity / float64(steps)
	drift := (opt.Rate - opt.DivYield - 0.5*opt.Vol*opt.Vol) * dt
	diff := opt.Vol * math.Sqrt(dt)
	disc := math.Exp(-opt.Rate * opt.Maturity)
	if math.IsNaN(drift) || math.IsInf(drift, 0) ||
		math.IsNaN(diff) || math.IsInf(diff, 0) ||
		math.IsNaN(disc) || math.IsInf(disc, 0) {
		return 0, &Error{Kind: ErrNonFinite, Detail: "non-finite GBM coefficients"}
	}

	s := opt.Spot
	logS := math.Log(s)
	sumS := 0.0
	breached := false
	for j := 0; j < steps; j++ {
		prevLogS := logS
		logS += drift + diff*sign*zs[j]
		s = math.Exp(logS)
		if math.IsInf(s, 0) || math.IsNaN(s) {
			return 0, &Error{Kind: ErrNonFinite, Detail: "spot path became non-finite"}
		}
		sumS += s
		if opt.Type == Barrier && !breached {
			breached = barrierHit(opt, math.Exp(prevLogS), s, dt)
		}
	}

	var payoff float64
	switch opt.Type {
	case European:
		payoff = opt.intrinsic(s)
	case Asian:
		avg := (opt.Spot + sumS) / float64(steps+1)
		payoff = opt.intrinsic(avg)
	case Barrier:
		alive := opt.Barrier.Kind == KnockOut && !breached ||
			opt.Barrier.Kind == KnockIn && breached
		if alive {
			payoff = opt.intrinsic(s)
		}
	}
	pv := disc * payoff
	if math.IsNaN(pv) || math.IsInf(pv, 0) {
		return 0, &Error{Kind: ErrNonFinite, Detail: "payoff became non-finite"}
	}
	return pv, nil
}

// barrierHit reports whether the barrier was breached on this step.
func barrierHit(opt *Option, sPrev, sNext, dt float64) bool {
	b := opt.Barrier
	if b.Monitoring == MonitorDiscrete {
		if b.Direction == Up {
			return sNext >= b.Level
		}
		return sNext <= b.Level
	}
	// Continuous monitoring: Brownian-bridge crossing probability for
	// the log-price between the two simulated dates.
	if b.Direction == Up && (sPrev >= b.Level || sNext >= b.Level) {
		return true
	}
	if b.Direction == Down && (sPrev <= b.Level || sNext <= b.Level) {
		return true
	}
	if opt.Vol == 0 {
		return false
	}
	x1, x2 := math.Log(sPrev), math.Log(sNext)
	h := math.Log(b.Level)
	pCross := math.Exp(-2 * (h - x1) * (h - x2) / (opt.Vol * opt.Vol * dt))
	// Draw deterministically from the step index is not available here;
	// use a hash of the endpoints so the decision is deterministic and
	// path-independent.
	u := deterministicUniform(x1, x2)
	return u < pCross
}

// deterministicUniform maps two floats to a stable pseudo-uniform in
// [0,1) without consuming RNG state.
func deterministicUniform(a, b float64) float64 {
	bits := math.Float64bits(a)*0x9e3779b97f4a7c15 ^ math.Float64bits(b)
	bits = splitmix64(bits)
	return float64(bits>>11) * (1.0 / (1 << 53))
}

// BatchResult pairs a Result with its input index.
type BatchResult struct {
	Index  int
	Result Result
	Err    error
}

// PriceBatch prices many options in parallel. Determinism: each
// option's seed is derived from cfg.Seed and a hash of the option
// itself, so results are bit-identical for any MaxWorkers and do not
// depend on the position of a task in the batch.
func (e *Engine) PriceBatch(opts []Option, cfg Config) []BatchResult {
	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(opts) {
		workers = len(opts)
	}
	if workers < 1 {
		workers = 1
	}
	results := make([]BatchResult, len(opts))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				taskCfg := cfg
				taskCfg.Seed = splitmix64(cfg.Seed ^ hashOption(&opts[i]))
				res, err := e.Price(opts[i], taskCfg)
				results[i] = BatchResult{Index: i, Result: res, Err: err}
			}
		}()
	}
	for i := range opts {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// hashOption derives a stable 64-bit fingerprint of all fields that
// influence pricing.
func hashOption(o *Option) uint64 {
	h := uint64(0xcbf29ce484222325)
	mix := func(v uint64) {
		h ^= v
		h *= 0x100000001b3
	}
	mix(uint64(o.Type))
	mix(uint64(o.Payoff))
	for _, f := range []float64{o.Spot, o.Strike, o.Maturity, o.Rate, o.DivYield, o.Vol, o.Barrier.Level} {
		mix(math.Float64bits(f))
	}
	mix(uint64(o.Barrier.Direction))
	mix(uint64(o.Barrier.Kind))
	mix(uint64(o.Barrier.Monitoring))
	return h
}
