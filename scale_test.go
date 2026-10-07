package mcengine

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestLargeCalendarPerformance makes sure a contract with a thousand
// observation points does not blow up runtime or memory: the merged
// timeline has at most NumSteps + len(calendar) segments, and buffers
// are allocated once per worker.
func TestLargeCalendarPerformance(t *testing.T) {
	dates := make([]float64, 1500)
	for i := range dates {
		dates[i] = float64(i+1) / 1500.0
	}
	for _, tc := range []struct {
		name string
		opt  Option
	}{
		{"asian", func() Option {
			o := baseOption()
			o.Type = Asian
			o.ObservationDates = dates
			return o
		}()},
		{"discrete barrier", func() Option {
			o := baseOption()
			o.Type = Barrier
			o.Barrier = BarrierSpec{Level: 118, Direction: Up, Kind: KnockOut, Monitoring: MonitorDiscrete}
			o.ObservationDates = dates
			return o
		}()},
		{"discrete lookback", func() Option {
			o := baseOption()
			o.Type = Lookback
			o.Lookback = LookbackSpec{Kind: FloatingStrike, Monitoring: MonitorDiscrete}
			o.ObservationDates = dates
			return o
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ms0, ms1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&ms0)
			start := time.Now()
			res, err := NewEngine().Price(tc.opt,
				Config{NumPaths: 40000, NumSteps: 256, Seed: 4, Antithetic: true, MaxWorkers: 4})
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start)
			runtime.ReadMemStats(&ms1)
			alloc := ms1.TotalAlloc - ms0.TotalAlloc
			t.Logf("1500 calendar points, %d paths: price=%.4f se=%.5f time=%v alloc=%.1fMB",
				res.Paths, res.Price, res.StdErr, elapsed, float64(alloc)/(1<<20))
			if elapsed > 20*time.Second {
				t.Fatalf("large-calendar pricing too slow: %v", elapsed)
			}
			if alloc > 256<<20 {
				t.Fatalf("large-calendar pricing allocated too much: %d bytes", alloc)
			}
		})
	}
}

// TestLargeBatchDeterminism runs a 500-contract batch twice (different
// worker caps) and checks every result is bit-identical.
func TestLargeBatchDeterminism(t *testing.T) {
	const n = 500
	build := func(workers int) []BatchTask {
		tasks := make([]BatchTask, n)
		for i := range tasks {
			o := baseOption()
			o.Strike = 85 + float64(i%30)
			switch i % 4 {
			case 1:
				o.Type = Asian
				o.ObservationDates = []float64{0.25, 0.5, 0.75, 1}
			case 2:
				o.Type = Barrier
				o.Barrier = BarrierSpec{Level: 130, Direction: Up, Kind: KnockIn, Monitoring: MonitorContinuous}
			case 3:
				o.Type = Lookback
				o.Lookback = LookbackSpec{Kind: FixedStrike, Monitoring: MonitorDiscrete}
				o.ObservationDates = linDates(12, 1)
			}
			tasks[i] = BatchTask{
				Option: o,
				Config: Config{
					NumPaths:   2000 + (i%3)*1000,
					NumSteps:   24 + (i%2)*24,
					Seed:       17,
					Antithetic: i%2 == 1,
					MaxWorkers: workers,
				},
			}
		}
		return tasks
	}
	eng := NewEngine()
	one := eng.PriceBatchTasks(context.Background(), build(1))
	many := eng.PriceBatchTasks(context.Background(), build(8))
	for i := range one {
		if one[i].Err != many[i].Err {
			t.Fatalf("task %d error mismatch", i)
		}
		if one[i].Result != many[i].Result {
			t.Fatalf("task %d not bit-identical", i)
		}
	}
	t.Logf("500-contract batch bit-identical at 1 and 8 workers")
}
