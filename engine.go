// Package mcengine provides a fully local Monte Carlo option pricing
// library. It uses no external market data or services: all pricing
// parameters are supplied by the caller and all randomness is generated
// locally from explicit seeds.
package mcengine

import (
	"context"
	"hash/fnv"
	"math"
	"runtime"
	"sync"
)

// DefaultMinPaths is the floor on NumPaths when Config.MinPaths is 0.
const DefaultMinPaths = 1000

// Config controls one pricing run. A zero Config is invalid; at
// minimum NumPaths, NumSteps and Seed should be set (Seed may be 0).
type Config struct {
	// NumPaths is the number of simulated paths. With Antithetic on,
	// this is the number of antithetic pairs.
	NumPaths int
	// NumSteps is the number of equally spaced time steps per path.
	// With an explicit ObservationDates calendar it controls
	// numerical resolution only and never changes the observation
	// convention; without a calendar it also defines the equally
	// spaced observation dates.
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

// randUint produces a stable uniform in [0,1) for an addressable
// stream element (path, segment, kind). Any worker can compute it
// independently, so parallel results are bit-identical regardless of
// how paths are partitioned.
func randUint(seed, path, seg, kind uint64) float64 {
	h := seed ^ (path*0x9e3779b97f4a7c15 + seg*0xbf58476d1ce4e5b9 + kind)
	h = splitmix64(h)
	return float64(h>>11) * (1.0 / (1 << 53))
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
	tl, err := buildTimeline(&opt, &cfg)
	if err != nil {
		return Result{}, err
	}
	n := cfg.NumPaths
	m := n
	if cfg.Antithetic {
		m = 2 * n
	}
	workers := cfg.MaxWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}

	// Split the path index space into fixed contiguous chunks. Every
	// chunk owns a deterministic sub-seed, so results are bit-identical
	// for any worker count; the final reduction follows a fixed order.
	// Per-path payoffs are written into preallocated slots, then
	// reduced in a single fixed (path index) order, which makes the
	// floating-point result bit-identical at any worker count.
	payoffs := make([]float64, m)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		w := w
		lo := w * n / workers
		hi := (w + 1) * n / workers
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			zs := make([]float64, len(tl.segments))
			us := make([]float64, len(tl.segments))
			for i := lo; i < hi; i++ {
				for j := range tl.segments {
					// Stream element (i,j) is addressable from any
					// worker, so results never depend on the split.
					zs[j] = normInv(randUint(cfg.Seed, uint64(i), uint64(j), 0))
					if tl.segments[j].barrierBB || tl.segments[j].lookBB {
						us[j] = randUint(cfg.Seed, uint64(i), uint64(j), 1)
					}
				}
				p, err := simulatePath(&opt, tl, zs, us, 1)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				payoffs[i] = p
				if cfg.Antithetic {
					pa, err := simulatePath(&opt, tl, zs, us, -1)
					if err != nil {
						select {
						case errCh <- err:
						default:
						}
						return
					}
					payoffs[n+i] = pa
				}
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return Result{}, err
	default:
	}
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

// segment is one interval of the simulation timeline: the union of
// the equally spaced numerical grid and the observation calendar.
type segment struct {
	dt        float64
	observe   bool // end point is an observation-calendar date
	barrierBB bool // apply Brownian-bridge continuous-barrier crossing
	lookBB    bool // draw continuous-lookback segment extremum
}

// timeline holds the merged simulation schedule.
type timeline struct {
	segments []segment
	nObs     int // number of observation-calendar dates
}

// defaultCalendar returns the legacy equally spaced observation
// schedule when no explicit calendar is supplied.
func defaultCalendar(opt *Option, cfg *Config) []float64 {
	n := cfg.NumSteps
	dates := make([]float64, 0, n+1)
	if opt.Type == Asian {
		dates = append(dates, 0)
	}
	for j := 1; j <= n; j++ {
		dates = append(dates, opt.Maturity*float64(j)/float64(n))
	}
	return dates
}

// buildTimeline merges the NumSteps grid with the observation
// calendar. Observation semantics come only from the calendar; grid
// refinement never adds or removes observation points. Segments use
// exact GBM increments, so calendar dates never need rounding.
func buildTimeline(opt *Option, cfg *Config) (*timeline, error) {
	grid := make([]float64, cfg.NumSteps+1)
	for j := 0; j <= cfg.NumSteps; j++ {
		grid[j] = opt.Maturity * float64(j) / float64(cfg.NumSteps)
	}
	if opt.Type == European {
		segs := make([]segment, cfg.NumSteps)
		for i := range segs {
			segs[i] = segment{dt: grid[1]}
		}
		return &timeline{segments: segs}, nil
	}

	calendar := opt.ObservationDates
	if calendar == nil {
		calendar = defaultCalendar(opt, cfg)
	}

	continuous := (opt.Type == Barrier && opt.Barrier.Monitoring == MonitorContinuous) ||
		(opt.Type == Lookback && opt.Lookback.Monitoring == MonitorContinuous)

	times := make([]float64, 0, cfg.NumSteps+len(calendar))
	times = append(times, 0)
	gi, ci := 1, 0
	for ci < len(calendar) && calendar[ci] == 0 {
		ci++
	}
	for gi <= cfg.NumSteps || ci < len(calendar) {
		tg, tc := math.Inf(1), math.Inf(1)
		if gi <= cfg.NumSteps {
			tg = grid[gi]
		}
		if ci < len(calendar) {
			tc = calendar[ci]
		}
		switch {
		case tc < tg:
			times = append(times, tc)
			ci++
		case tg < tc:
			times = append(times, tg)
			gi++
		default:
			times = append(times, tg)
			gi++
			ci++
		}
	}

	isCal := make(map[float64]bool, len(calendar))
	for _, d := range calendar {
		isCal[d] = true
	}
	segs := make([]segment, 0, len(times)-1)
	for k := 1; k < len(times); k++ {
		s := segment{dt: times[k] - times[k-1]}
		if isCal[times[k]] {
			s.observe = true
		}
		if continuous {
			if opt.Type == Barrier {
				s.barrierBB = true
			} else {
				s.lookBB = true
			}
		}
		segs = append(segs, s)
	}
	return &timeline{segments: segs, nObs: len(calendar)}, nil
}

// simulatePath evolves one exact-increment GBM path over the merged
// timeline and returns the discounted payoff. sign flips the normal
// increments and mirrors the uniforms for antithetic sampling.
func simulatePath(opt *Option, tl *timeline, zs, us []float64, sign float64) (float64, error) {
	disc := math.Exp(-opt.Rate * opt.Maturity)
	nu := opt.Rate - opt.DivYield - 0.5*opt.Vol*opt.Vol

	s := opt.Spot
	logS := math.Log(s)
	sumS := 0.0
	nFix := 0
	breached := false
	sMin, sMax := s, s

	if opt.Type == Asian && opt.ObservationDates == nil {
		// Legacy equally spaced Asian fixings include time 0.
		sumS = s
		nFix = 1
	}

	for j, seg := range tl.segments {
		prevLogS := logS
		drift := nu * seg.dt
		diff := opt.Vol * math.Sqrt(seg.dt)
		if math.IsNaN(drift) || math.IsInf(drift, 0) ||
			math.IsNaN(diff) || math.IsInf(diff, 0) {
			return 0, &Error{Kind: ErrNonFinite, Detail: "non-finite GBM coefficients"}
		}
		logS += drift + diff*sign*zs[j]
		s = math.Exp(logS)
		if math.IsInf(s, 0) || math.IsNaN(s) {
			return 0, &Error{Kind: ErrNonFinite, Detail: "spot path became non-finite"}
		}

		switch {
		case seg.barrierBB:
			if !breached && barrierBridgeHit(opt, prevLogS, logS, seg.dt, us[j], sign) {
				breached = true
			}
		case seg.lookBB:
			lo, hi := lookbackExtremes(prevLogS, logS, seg.dt, opt.Vol, us[j], sign)
			if lo < sMin {
				sMin = lo
			}
			if hi > sMax {
				sMax = hi
			}
		}

		if seg.observe {
			switch opt.Type {
			case Asian:
				sumS += s
				nFix++
			case Barrier:
				if opt.Barrier.Monitoring == MonitorDiscrete && !breached {
					b := opt.Barrier
					if (b.Direction == Up && s >= b.Level) || (b.Direction == Down && s <= b.Level) {
						breached = true
					}
				}
			case Lookback:
				if opt.Lookback.Monitoring == MonitorDiscrete {
					if s < sMin {
						sMin = s
					}
					if s > sMax {
						sMax = s
					}
				}
			}
		}
	}

	var payoff float64
	switch opt.Type {
	case European:
		payoff = opt.intrinsic(s)
	case Asian:
		payoff = opt.intrinsic(sumS / float64(nFix))
	case Barrier:
		alive := opt.Barrier.Kind == KnockOut && !breached ||
			opt.Barrier.Kind == KnockIn && breached
		if alive {
			payoff = opt.intrinsic(s)
		}
	case Lookback:
		switch opt.Lookback.Kind {
		case FloatingStrike:
			if opt.Payoff == Call {
				payoff = s - sMin
			} else {
				payoff = sMax - s
			}
		case FixedStrike:
			if opt.Payoff == Call {
				payoff = math.Max(sMax-opt.Strike, 0)
			} else {
				payoff = math.Max(opt.Strike-sMin, 0)
			}
		}
	}
	pv := disc * payoff
	if math.IsNaN(pv) || math.IsInf(pv, 0) {
		return 0, &Error{Kind: ErrNonFinite, Detail: "payoff became non-finite"}
	}
	return pv, nil
}

// barrierBridgeHit applies the Brownian-bridge crossing probability
// for the log-price between two simulated dates (continuous
// monitoring). Crossing draws come from the RNG stream so decisions
// stay unbiased as the grid is refined; the antithetic sign mirrors
// the uniform.
func barrierBridgeHit(opt *Option, x1, x2, dt, u, sign float64) bool {
	b := opt.Barrier
	h := math.Log(b.Level)
	d1, d2 := h-x1, h-x2
	if b.Direction == Up {
		if d1 <= 0 || d2 <= 0 {
			return true
		}
	} else {
		if d1 >= 0 || d2 >= 0 {
			return true
		}
	}
	if opt.Vol == 0 {
		return false
	}
	uu := u
	if sign < 0 {
		uu = 1 - u
	}
	pCross := math.Exp(-2 * d1 * d2 / (opt.Vol * opt.Vol * dt))
	return uu < pCross
}

// lookbackExtremes samples the running minimum and maximum of a
// Brownian bridge starting at log-price x1 and ending at x2. For a
// standardized bridge endpoint z, write the maximum as
// M = max(z, 0) + E. Conditional on crossing the higher endpoint, the
// overshoot E is exponential: with U ~ Unif(0,1),
//
//	P(cross) = exp(-2 m0 |z|),  E = -0.5 ln(V) + correction
//
// implemented exactly by inverting the known bridge maximum CDF
//
//	P(M <= m) = Phi(m) - exp(-2m(m-z)) Phi(m-2z),  m >= m0
//
// through a single normal inverse. The minimum follows by sign
// symmetry; the uniform is mirrored for the antithetic path.
func lookbackExtremes(x1, x2, dt, sig, u, sign float64) (lo, hi float64) {
	if sig == 0 {
		return math.Exp(math.Min(x1, x2)), math.Exp(math.Max(x1, x2))
	}
	uu := u
	if sign < 0 {
		uu = 1 - u
	}
	s := sig * math.Sqrt(dt)
	z := (x2 - x1) / s
	hiM := bridgeMax(z, uu)
	loM := -bridgeMax(-z, uu)
	hiLog := x1 + s*hiM
	loLog := x1 + s*loM
	return math.Exp(loLog), math.Exp(hiLog)
}

// bridgeMax returns the sampled standardized maximum of a Brownian
// bridge from 0 to z. With m0 = max(z,0), the probability of reaching
// above m0 is p = exp(-2 m0 (m0-z)); conditional on a crossing, the
// overshoot E = M - m0 satisfies
//
//	1 - V = exp(-2 E (E + 2 m0 - z)),  V ~ Unif(0,1)
//
// which is inverted with one square root and no normal CDF calls.
func bridgeMax(z, u float64) float64 {
	m0 := math.Max(z, 0)
	p := math.Exp(-2 * m0 * (m0 - z))
	if u >= p {
		return m0
	}
	// Crossing: draw V from the conditional V < p via u/p (shared
	// uniform keeps antithetic pairing exact), then
	// E^2 + (2 m0 - z) E + 0.5 ln(1-V) = 0.
	v := u / p
	c := 2*m0 - z
	disc := c*c - 2*math.Log1p(-v)
	e := (-c + math.Sqrt(disc)) / 2
	if e < 0 {
		e = 0
	}
	return m0 + e
}

// BatchTask is one self-contained pricing request inside a batch.
// Each contract carries its own calendar (on the Option) and its own
// Config, so path counts, steps, antithetic switch and tolerance can
// differ across the table.
type BatchTask struct {
	Option Option
	Config Config
}

// BatchResult pairs a Result with its input index. A failed contract
// has Result == Result{} and Err carrying a classifiable *Error
// (ErrInvalidParam, ErrTooFewPaths, ErrNotConverged, ErrNonFinite or
// ErrCanceled); other contracts are unaffected.
type BatchResult struct {
	Index  int
	Result Result
	Err    error
}

// PriceBatch prices many options in parallel with one shared Config.
// Each task's effective seed is derived from cfg.Seed and a fingerprint
// of the option, so results are bit-identical for any MaxWorkers and
// do not depend on task position or on neighboring tasks.
func (e *Engine) PriceBatch(opts []Option, cfg Config) []BatchResult {
	tasks := make([]BatchTask, len(opts))
	for i := range opts {
		tasks[i] = BatchTask{Option: opts[i], Config: cfg}
	}
	return e.PriceBatchTasks(context.Background(), tasks)
}

// PriceBatchTasks is the real-backtest entry point: every contract
// carries its own Option (including its observation calendar) and its
// own Config. A contract's result depends only on that contract's
// inputs and cfg.Seed; changing any other contract never changes it.
// Invalid or numerically failed contracts fail individually with a
// classified error while the rest of the batch still completes.
//
// Canceling ctx stops launching new work as soon as possible;
// in-flight contracts run to completion and their results stay
// available, while not-started contracts come back with ErrCanceled.
func (e *Engine) PriceBatchTasks(ctx context.Context, tasks []BatchTask) []BatchResult {
	workers := 0
	for i := range tasks {
		if w := tasks[i].Config.MaxWorkers; w > workers {
			workers = w
		}
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers < 1 {
		workers = 1
	}

	results := make([]BatchResult, len(tasks))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := make(map[int]bool, len(tasks))
	var closeOnce sync.Once
	finish := func(canceledMark bool) {
		closeOnce.Do(func() { close(jobs) })
		wg.Wait()
		if canceledMark {
			canceled := &Error{Kind: ErrCanceled, Detail: "batch canceled before pricing"}
			mu.Lock()
			for k := range tasks {
				if !done[k] {
					results[k] = BatchResult{Index: k, Err: canceled}
				}
			}
			mu.Unlock()
		}
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				select {
				case <-ctx.Done():
					continue
				default:
				}
				taskCfg := tasks[i].Config
				taskCfg.Seed = deriveSeed(taskCfg.Seed, &tasks[i].Option)
				res, err := e.Price(tasks[i].Option, taskCfg)
				mu.Lock()
				results[i] = BatchResult{Index: i, Result: res, Err: err}
				done[i] = true
				mu.Unlock()
			}
		}()
	}
feed:
	for i := range tasks {
		select {
		case <-ctx.Done():
			finish(true)
			break feed
		case jobs <- i:
		}
	}
	finish(false)
	return results
}

// deriveSeed combines the caller seed with a fingerprint of all option
// fields that influence the price. Bit-identical inputs always map to
// the same seed regardless of position.
func deriveSeed(base uint64, o *Option) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	put := func(v uint64) {
		for i := 0; i < 8; i++ {
			buf[i] = byte(v >> (uint(i) * 8))
		}
		_, _ = h.Write(buf[:])
	}
	put(uint64(o.Type))
	put(uint64(o.Payoff))
	for _, f := range []float64{o.Spot, o.Strike, o.Maturity, o.Rate, o.DivYield, o.Vol} {
		put(math.Float64bits(f))
	}
	put(math.Float64bits(o.Barrier.Level))
	put(uint64(o.Barrier.Direction))
	put(uint64(o.Barrier.Kind))
	put(uint64(o.Barrier.Monitoring))
	put(uint64(o.Lookback.Kind))
	put(uint64(o.Lookback.Monitoring))
	put(uint64(len(o.ObservationDates)))
	for _, d := range o.ObservationDates {
		put(math.Float64bits(d))
	}
	return splitmix64(base ^ h.Sum64())
}

// hashOption is retained as a thin wrapper over deriveSeed for
// callers/tests that only need a fingerprint.
func hashOption(o *Option) uint64 { return deriveSeed(0, o) }
