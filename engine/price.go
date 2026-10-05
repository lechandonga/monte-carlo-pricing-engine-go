// Package engine is the single Monte Carlo pricing entry point for European,
// Asian (arithmetic average) and single-barrier (knock-in/knock-out,
// continuous or discrete monitoring) options.
//
// # Determinism contract
//
// Every simulated path derives its own random stream from (Seed, pathIndex).
// Path chunks may therefore run on any goroutine, in any order; partial
// statistics are merged in fixed index order. Equal (Params, Config) inputs
// with equal Seed give bit-identical Price/StdErr/confidence interval for
// any Workers value. Batch jobs derive independent seeds from (Seed, job
// ordinal), so reordering jobs never changes any individual result.
package engine

import (
	"math"
	"sync"
	"time"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

// Price is the unified pricing entry point. Analytical scenarios are not
// silently substituted: the Monte Carlo estimator is always returned so its
// standard error and confidence interval are available; closed-form values
// live in the analytics package for comparison and control variates.
func Price(p option.Params, cfg Config) (option.Result, error) {
	if err := cfg.validate(p); err != nil {
		return option.Result{}, err
	}
	start := time.Now()

	chunks := planChunks(cfg.Paths, cfg.Workers, cfg.BatchSize, cfg.Antithetic)
	results := make([]batchStats, len(chunks))

	workers := cfg.workers()
	if workers == 1 || len(chunks) == 1 {
		for idx, ch := range chunks {
			results[idx] = simulateBatch(p, cfg, ch.start, ch.end)
		}
	} else {
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for idx, ch := range chunks {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, ch pathRange) {
				defer wg.Done()
				defer func() { <-sem }()
				results[idx] = simulateBatch(p, cfg, ch.start, ch.end)
			}(idx, ch)
		}
		wg.Wait()
	}

	price, stderr, lo, hi, nonFinite, n, err := aggregate(results, cfg)
	res := option.Result{
		Price:     price,
		StdErr:    stderr,
		CI95Low:   lo,
		CI95High:  hi,
		Paths:     n,
		Steps:     p.Steps,
		Duration:  time.Since(start),
		UsedCV:    cfg.ControlVariate && p.Kind == option.Asian,
		NonFinite: nonFinite,
		Converged: err == nil,
		Err:       err,
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

type pathRange struct{ start, end int }

// planChunks splits [0,paths) into index-ordered ranges. Antithetic ranges
// always stay pair-aligned (multiples of two) so each pair keeps one stream.
func planChunks(paths, workers, batchSize int, antithetic bool) []pathRange {
	if batchSize <= 0 {
		// Fixed target chunk count independent of the worker count, so the
		// set of partial sums fed to the deterministic tree reduction is
		// identical for any Workers value.
		const targetChunks = 64
		batchSize = paths / targetChunks
		if batchSize < 1 {
			batchSize = paths
		}
		if antithetic {
			batchSize = max(2, batchSize&^1)
		}
	}
	var out []pathRange
	for s := 0; s < paths; {
		e := s + batchSize
		if e > paths {
			e = paths
		}
		out = append(out, pathRange{s, e})
		s = e
	}
	return out
}

// PriceBatch prices many requests using up to cfg.Workers goroutines
// (global cap; 0/negative means GOMAXPROCS). Tagged jobs receive a seed
// derived from (cfg.Seed, Tag), so changing the slice order or the worker
// count leaves every per-job output bit-identical; untagged jobs are seeded
// by ordinal and thus must keep a stable position in the caller's slice.
func PriceBatch(reqs []option.Request, cfg Config) []option.Result {
	results := make([]option.Result, len(reqs))
	workers := cfg.workers()
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, req := range reqs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, req option.Request) {
			defer wg.Done()
			defer func() { <-sem }()
			jobCfg := cfg
			if req.Tag != "" {
				jobCfg.Seed = taggedSeed(cfg.Seed, req.Tag)
			} else {
				jobCfg.Seed = jobSeed(cfg.Seed, i)
			}
			r, err := Price(req.Params, jobCfg)
			r.Tag = req.Tag
			r.Err = err
			results[i] = r
		}(i, req)
	}
	wg.Wait()
	return results
}

// PriceAnalytic returns the closed-form reference price for products that
// have one (European; continuous-monitoring barrier; geometric Asian only for
// Asian). For arithmetic Asians no closed form exists and ok is false.
func PriceAnalytic(p option.Params) (float64, bool) {
	if err := p.Validate(); err != nil {
		return math.NaN(), false
	}
	call := p.Right == option.Call
	switch p.Kind {
	case option.European:
		return analyticsBlackScholes(p, call), true
	case option.Barrier:
		if p.Monitoring == option.Discrete {
			return math.NaN(), false
		}
		return analyticsBarrier(p, call), true
	case option.Asian:
		// Only geometric Asians are analytical; the engine prices arithmetic
		// averaging, so no misleading reference is returned.
		return math.NaN(), false
	}
	return math.NaN(), false
}
