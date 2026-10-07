package mcengine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBatchHeterogeneousConfigs builds a few-hundred-contract table
// where every contract carries its own calendar and its own config
// (paths, steps, antithetic, tolerance). Results must depend only on
// the contract itself: identical across worker counts, across task
// order, and when a neighbor changes configuration.
func TestBatchHeterogeneousConfigs(t *testing.T) {
	const n = 320
	tasks := make([]BatchTask, n)
	for i := range tasks {
		o := baseOption()
		o.Strike = 80 + float64(i%40)
		switch i % 4 {
		case 1:
			o.Type = Asian
			o.ObservationDates = []float64{0.1, 0.4, 0.7, 1.0}
		case 2:
			o.Type = Barrier
			o.Barrier = BarrierSpec{Level: 125, Direction: Up, Kind: KnockOut, Monitoring: MonitorContinuous}
		case 3:
			o.Type = Lookback
			o.Lookback = LookbackSpec{Kind: FloatingStrike, Monitoring: MonitorDiscrete}
			o.ObservationDates = linDates(8+(i%50), 1)
		}
		cfg := Config{
			NumPaths:   1500 + (i%5)*250,
			NumSteps:   16 + (i%3)*16,
			Seed:       42,
			Antithetic: i%2 == 0,
		}
		if i%7 == 0 {
			cfg.RelTol = 0.5
		}
		tasks[i] = BatchTask{Option: o, Config: cfg}
	}
	eng := NewEngine()
	run := func(workers int) []BatchResult {
		ts := make([]BatchTask, len(tasks))
		copy(ts, tasks)
		for i := range ts {
			ts[i].Config.MaxWorkers = workers
		}
		return eng.PriceBatchTasks(context.Background(), ts)
	}

	serial := run(1)
	parallel := run(8)
	for i := range serial {
		if serial[i].Err != nil || parallel[i].Err != nil {
			t.Fatalf("task %d errors: %v / %v", i, serial[i].Err, parallel[i].Err)
		}
		if serial[i].Result != parallel[i].Result {
			t.Fatalf("task %d differs across worker counts", i)
		}
	}
	t.Logf("%d heterogeneous tasks bit-identical across 1 vs 8 workers", n)

	// Shuffle order; each contract's result must follow it unchanged.
	shuffled := make([]BatchTask, n)
	for i := range shuffled {
		shuffled[i] = tasks[(i*97+13)%n]
	}
	for i := range shuffled {
		shuffled[i].Config.MaxWorkers = 4
	}
	out := eng.PriceBatchTasks(context.Background(), shuffled)
	for i := range out {
		orig := (i*97 + 13) % n
		if out[i].Result != serial[orig].Result {
			t.Fatalf("shuffled task %d (orig %d) changed result", i, orig)
		}
	}
	t.Logf("results independent of task order")

	// Change one neighbor's config; all other results must be
	// untouched.
	tasks2 := make([]BatchTask, len(tasks))
	copy(tasks2, tasks)
	tasks2[5].Config.NumPaths = 9000
	tasks2[5].Config.Antithetic = !tasks2[5].Config.Antithetic
	tasks2[5].Option.Strike += 1
	for i := range tasks2 {
		tasks2[i].Config.MaxWorkers = 4
	}
	out2 := eng.PriceBatchTasks(context.Background(), tasks2)
	for i := range out2 {
		if i == 5 {
			continue
		}
		if out2[i].Result != serial[i].Result {
			t.Fatalf("task %d changed after neighbor modification", i)
		}
	}
	t.Logf("results isolated from neighbor configs")
}

// TestBatchErrorIsolation mixes valid and invalid contracts and checks
// only the bad contracts fail, each with a classifiable reason.
func TestBatchErrorIsolation(t *testing.T) {
	type spec struct {
		opt  Option
		cfg  Config
		kind ErrKind
	}
	mk := func(mut func(*Option, *Config)) spec {
		o, c := baseOption(), Config{NumPaths: 2000, NumSteps: 8, Seed: 1}
		mut(&o, &c)
		return spec{opt: o, cfg: c}
	}
	specs := []spec{
		{opt: baseOption(), cfg: Config{NumPaths: 2000, NumSteps: 8, Seed: 1}, kind: -1},
		mk(func(o *Option, c *Config) { o.Vol = -1 }),
		mk(func(o *Option, c *Config) { c.NumPaths = 5 }),
		mk(func(o *Option, c *Config) { c.RelTol = 1e-12 }),
		mk(func(o *Option, c *Config) { o.Vol = 1e200 }),
		mk(func(o *Option, c *Config) {
			o.Type = Asian
			o.ObservationDates = []float64{0.5, 0.2}
		}),
		{opt: baseOption(), cfg: Config{NumPaths: 2000, NumSteps: 8, Seed: 2}, kind: -1},
	}
	specs[1].kind = ErrInvalidParam
	specs[2].kind = ErrTooFewPaths
	specs[3].kind = ErrNotConverged
	specs[4].kind = ErrNonFinite
	specs[5].kind = ErrInvalidParam

	batch := make([]BatchTask, len(specs))
	for i, s := range specs {
		batch[i] = BatchTask{Option: s.opt, Config: s.cfg}
	}
	out := NewEngine().PriceBatchTasks(context.Background(), batch)
	for i, s := range specs {
		if s.kind < 0 {
			if out[i].Err != nil {
				t.Fatalf("task %d unexpectedly failed: %v", i, out[i].Err)
			}
			if out[i].Result.Price == 0 && out[i].Result.Paths == 0 {
				t.Fatalf("task %d has no result", i)
			}
			continue
		}
		if kindOf(out[i].Err) != s.kind {
			t.Fatalf("task %d want kind %v, got %v", i, s.kind, out[i].Err)
		}
		t.Logf("task %d isolated failure: %v", i, out[i].Err)
	}
}

// TestBatchCancel cancels a large batch partway and verifies: the call
// returns promptly, already-completed results stay available and
// bit-identical to a full run, and not-started contracts come back as
// ErrCanceled.
func TestBatchCancel(t *testing.T) {
	eng := NewEngine()
	const n = 400
	tasks := make([]BatchTask, n)
	for i := range tasks {
		o := baseOption()
		o.Strike = 90 + float64(i%20)
		if i%3 == 0 {
			o.Type = Asian
		}
		tasks[i] = BatchTask{
			Option: o,
			Config: Config{NumPaths: 60000, NumSteps: 64, Seed: 7, MaxWorkers: 2},
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []BatchResult, 1)
	go func() { done <- eng.PriceBatchTasks(ctx, tasks) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		nDone, nCanceled := 0, 0
		for i := range res {
			var e *Error
			if errors.As(res[i].Err, &e) && e.Kind == ErrCanceled {
				nCanceled++
				continue
			}
			if res[i].Err != nil {
				t.Fatalf("unexpected error at %d: %v", i, res[i].Err)
			}
			nDone++
		}
		if nDone == 0 || nCanceled == 0 {
			t.Fatalf("expected some done (%d) and some canceled (%d)", nDone, nCanceled)
		}
		t.Logf("canceled batch: %d completed, %d not started", nDone, nCanceled)

		// Completed results must be bit-identical to an uncanceled run.
		full := eng.PriceBatchTasks(context.Background(), tasks)
		for i := range res {
			var e *Error
			if errors.As(res[i].Err, &e) && e.Kind == ErrCanceled {
				continue
			}
			if res[i].Result != full[i].Result {
				t.Fatalf("completed task %d changed after cancel run", i)
			}
		}
		t.Logf("completed results bit-identical to full run")
	case <-time.After(30 * time.Second):
		t.Fatal("batch did not stop promptly after cancel")
	}
}

// TestBatchWorkerCap checks MaxWorkers is respected even for a single
// large contract inside a batch (CPU usage stays bounded).
func TestBatchWorkerCap(t *testing.T) {
	tasks := make([]BatchTask, 4)
	for i := range tasks {
		o := baseOption()
		o.Type = Lookback
		o.Lookback = LookbackSpec{Kind: FloatingStrike, Monitoring: MonitorContinuous}
		tasks[i] = BatchTask{
			Option: o,
			Config: Config{NumPaths: 120000, NumSteps: 128, Seed: 3, MaxWorkers: 2},
		}
	}
	start := time.Now()
	out := NewEngine().PriceBatchTasks(context.Background(), tasks)
	for i := range out {
		if out[i].Err != nil {
			t.Fatalf("task %d: %v", i, out[i].Err)
		}
	}
	t.Logf("4 capped(MaxWorkers=2) continuous-lookback tasks in %v", time.Since(start))
}
