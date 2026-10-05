# Monte Carlo Pricing Engine (Go)

A completely **local** option pricing library for pre-back-test research:
pricing parameters are supplied by the caller, random numbers are generated
in-process (Go `math/rand/v2`, PCG), and **no external market data or remote
service is ever contacted**.

European, Asian and barrier options are all priced through the same entry
point (`engine.Price`), with closed-form references, confidence intervals,
optional variance reduction, numerical Greeks and parallel batch pricing.

## Packages

| Package | Responsibility |
| --- | --- |
| `option` | Product types, parameters, validation rules, typed error codes |
| `analytics` | Closed-form references: Black–Scholes, Reiner–Rubinstein barriers, geometric Asian |
| `engine` | Monte Carlo pricing, variance reduction, Greeks, deterministic parallel scheduling |
| `cmd/pricing-example` | Runnable end-to-end demonstration |

## Supported products

All products assume a single underlying following geometric Brownian motion
under risk-neutral drift `r - q` (continuous dividend / foreign rate yield
`q`).

- **European** call/put. Terminal payoff `max(S_T - K, 0)` / `max(K - S_T, 0)`.
- **Asian** arithmetic-average call/put, averaging over the step-grid fixings
  `t_1 … t_n = T` (monitoring at each simulated step).
- **Single barrier** call/put, up/down × knock-in/knock-out, with an optional
  fixed rebate. Two explicit monitoring semantics:
  - `Continuous` — the simulation uses the
    Broadie–Glasserman–Kou **Bernoulli bridge**: conditional on two
    consecutive grid prices, it compares a dedicated uniform against the
    exact conditional probability that the continuous path touched the
    barrier within the interval. This makes the discrete simulator an
    (unbiased) estimator of continuous monitoring instead of merely a
    coarse approximation. Rebates for knock-outs are paid as if at the hit.
  - `Discrete` — the barrier is observed **only** at step-grid points
    (steps `1 … n`, including maturity). Touching between fixings does not
    trigger. As the grid is refined, discrete prices converge to the
    continuous-monitoring limit.

Knock-in pays the vanilla payoff if the barrier was touched (rebate at
expiry otherwise); knock-out pays the vanilla payoff only if it was never
touched (rebate on the knock-out event).

## Parameters (`option.Params`)

| Field | Meaning | Constraint |
| --- | --- | --- |
| `Kind` | `European` / `Asian` / `Barrier` | required |
| `Right` | `Call` / `Put` | required |
| `Spot`, `Strike`, `Barrier`, `Rebate` | price levels in your currency unit | spot/strike/barrier `> 0` & finite; rebate `>= 0` |
| `T` | time to maturity, years | finite, **strictly positive** |
| `R`, `Q` | continuously compounded rate, dividend yield | finite (any sign) |
| `Vol` | volatility, decimal (`0.2` = 20%) | finite, **non-negative** (`0` allowed) |
| `Steps` | time steps per path | `>= 1` |
| `Direction` | `Up` / `Down` | up-barrier must be **strictly above** spot; down-barrier strictly below; equality at `t=0` is rejected |
| `BarType` | `KnockIn` / `KnockOut` | required for barriers |
| `Monitoring` | `Continuous` / `Discrete` | required for barriers |

Barrier/strike contradictions are rejected. In particular an up-and-out
**call** requires `Barrier > Strike` (it can never pay otherwise), and a
down-and-out **put** requires `Barrier < Strike`; these degenerate contracts
return `INVALID_BARRIER_CONFIG` instead of a meaningless number.

Invalid input is never repaired and never yields a default value:
`Params.Validate` and `engine.Price` return an error carrying one of the
typed codes below.

## Error classification (`option.ErrorCode`)

Switch on `option.ErrorCodeOf(err)`; do not parse message text.

| Code | Raised when |
| --- | --- |
| `INVALID_PARAMS` | unknown enum, non-positive/non-finite spot or strike, non-finite rate/yield, negative rebate |
| `NEGATIVE_VOLATILITY` | `Vol < 0` or non-finite |
| `NON_POSITIVE_MATURITY` | `T <= 0` or non-finite |
| `INVALID_STEPS` | `Steps <= 0` |
| `INVALID_BARRIER_CONFIG` | missing/contradictory barrier fields, barrier on the wrong side of spot, or the barrier/strike contradictions above |
| `INVALID_SIM_CONFIG` | `Paths <= 0`, odd antithetic path count, negative/NaN `RelTol`, negative `Workers`/`BatchSize` |
| `TOO_FEW_PATHS` | finite effective samples below `MinPaths` (default 1024; antithetic counts complete pairs) |
| `NOT_CONVERGED` | the 95% CI half-width exceeds `RelTol * |Price|` |
| `NON_FINITE_RESULT` | price or standard error is NaN/±Inf |

`engine.Price` returns both a populated `Result` **and** the error on
statistical failures (`NOT_CONVERGED`, `TOO_FEW_PATHS`,
`NON_FINITE_RESULT`) so diagnostics (paths, SE, CI, non-finite count) remain
inspectable; the error is the contract that the number must not be trusted.

## Simulation configuration (`engine.Config`)

| Field | Meaning |
| --- | --- |
| `Paths` | number of payoff samples; must be even with antithetic variates |
| `Workers` | goroutine cap. `0` = `GOMAXPROCS`, `1` = fully sequential |
| `Seed` | uint64 seed of the deterministic stream |
| `Antithetic` | paired `±z` sampling (estimator is the per-pair mean) |
| `ControlVariate` | geometric-Asian analytic control for arithmetic Asians |
| `MinPaths` | minimum finite effective sample size (default 1024) |
| `RelTol` | optional convergence criterion on the 95% CI half-width |
| `BatchSize` | fixed chunk size; default targets 64 chunks independent of `Workers` |

### Error and convergence semantics

- The result reports `Price`, `StdErr` (standard error of the mean),
  `CI95Low/High` (`±1.95996·SE`), effective `Paths`, `Steps`, `NonFinite`
  count and `Converged`.
- Variance is computed with the subtraction-free one-pass formula
  `(E[X²] − E[X]²)`, with negative round-off clamped to zero.
- With `RelTol > 0`, a result whose CI half-width exceeds
  `RelTol·max(|Price|, 1e-12)` fails with `NOT_CONVERGED`.
- Every non-finite payoff is counted in `NonFinite` and excluded; if too few
  finite samples remain the call fails with `TOO_FEW_PATHS`; NaN/Inf
  statistics fail with `NON_FINITE_RESULT`.
- Path-dependent products have no silent "answer": convergence is
  demonstrated by refining `Steps` (see the tests) and by the discrete →
  continuous barrier gap shrinking with the grid.

### Variance reduction

- **Antithetic variates**: pairs share one normal stream, with the second
  leg negating every draw (including the Bernoulli-bridge uniforms used for
  continuous barriers). The standard error is computed over **pair means**
  with the pair variance, which is what makes the reduction visible and
  unbiased.
- **Control variate (Asians)**: the geometric-average Asian has a closed
  form (lognormal average); the adjusted payoff is
  `Y + (E[X] − X)` with `Y` the arithmetic and `X` the geometric payoff,
  keeping the expectation of `Y`. In the bundled scenario this cuts the
  standard error by roughly an order of magnitude.

Both switches are opt-in; a variance-reduced price must remain inside the
plain estimator's confidence band, and tests enforce both a substantial SE
reduction and no drift.

## Reproducibility and concurrency

- Each simulated path owns an RNG stream derived from `(Seed, pathIndex)`
  via a SplitMix64-mixed PCG seed. Path `i` therefore always consumes the
  same random numbers regardless of which goroutine runs it.
- Partial sums are merged through a **fixed binary-tree reduction** over a
  worker-independent chunk plan. Consequently, identical
  `(Params, Config, Seed)` inputs produce **bit-identical** `Price`,
  `StdErr` and CI endpoints for `Workers = 1`, `2`, `GOMAXPROCS`, any chunk
  size, and repeated runs.
- Changing `Seed` changes the stream; antithetic/control-variate runs have
  their own estimators and are bit-stable against their single-worker
  counterparts.
- `PriceBatch` caps total parallelism with `Workers`. A request with a
  non-empty `Tag` is seeded from `(Seed, Tag)` (FNV-1a + SplitMix64), so
  **reordering jobs or changing the worker count never changes a tagged
  job's output**. Untagged jobs fall back to ordinal seeding and must keep a
  stable slice position. Per-job parameter errors are returned on that
  job's `Result`; they do not abort the batch.

## Greeks (`engine.EstimateGreeks`)

Central finite differences on `Price` with full common random numbers
(every bumped price uses the same seed and therefore the same per-path
streams):

- Delta: ±1% spot; Gamma from the same three spot prices
- Vega: ±0.01 volatility point; Rho: ±1 bp rate; Theta: 1-day shorter maturity

Bump sizes are configurable via `GreekConfig`. Every Greek carries its own
standard error. CRN makes repeated calls and different worker counts return
the identical values; tests pin bit stability and compare against analytic
Black–Scholes Greeks.

## Quick start

```go
p := option.Params{Kind: option.Barrier, Right: option.Call,
    Spot: 100, Strike: 100, T: 1, R: 0.05, Vol: 0.2, Steps: 252,
    Direction: option.Up, BarType: option.KnockOut, Barrier: 120,
    Monitoring: option.Continuous}

r, err := engine.Price(p, engine.Config{
    Paths: 400000, Workers: 4, Seed: 42,
    Antithetic: true, ControlVariate: false, RelTol: 0.01,
})
if err != nil { /* typed option.ErrorCode via option.ErrorCodeOf(err) */ }
fmt.Println(r.Price, r.StdErr, r.CI95Low, r.CI95High)

closedForm, ok := engine.PriceAnalytic(p) // European or continuous barrier
```

Run the demonstration:

```sh
go run ./cmd/pricing-example
```

## Local verification

All verification is offline and deterministic.

```sh
# Unit tests (business logs show business area, case, inputs and result)
go test -count=1 ./...

# Race detector for the concurrency and batch tests
CGO_ENABLED=1 go test -race -count=1 ./...

# Verbose per-case logs (business=<area> case=<name> ... result=...)
go test -v -count=1 ./...
```

### Test coverage map

| Business area | Test | What it pins |
| --- | --- | --- |
| Parameter rejection | `option.TestInvalidParams` | every illegal case with its distinct `ErrorCode` |
| Analytic correctness | `analytics.TestBlackScholesReferenceValues`, `TestGeometricAsianSanity` | textbook values, put–call parity, geometric Asian with one fixing = Black–Scholes |
| Barrier formulas | `analytics.TestBarrierParity` | in+out = vanilla for all 8 contracts and both strike/barrier orderings |
| Main pricing flow | `engine.TestEuropeanAgreesWithAnalytic` | MC 95% CI covers the closed form, calls and puts |
| Barrier monitoring | `engine.TestBarrierAgreesWithAnalytic` | 8 continuously monitored contracts vs analytic |
| Path-dependent convergence | `engine.TestPathDependentConvergence` | Asian grid refinement stability; discrete→continuous barrier gap shrinks |
| Variance reduction | `engine.TestVarianceReduction` | SE materially drops with CV/antithetic; prices stay in the plain band |
| Determinism | `engine.TestSeedAndConcurrencyDeterminism` | bit-identical outputs across worker counts/chunk sizes/repeats; seeds independent |
| Parallel batches | `engine.TestBatchOrderIndependence`, `TestBatchPropagatesErrors` | tagged jobs order/worker-independent; per-job typed errors |
| Failure contracts | `engine.TestEngineErrorContracts` | too few paths, odd antithetic count, bad tolerance, non-convergence |
| Greek stability | `engine.TestGreeksStable` | bit stability across repeats/workers and agreement with analytic Greeks |
| Resource limiting | `engine.TestWorkersResourceLimit` | `Workers` cap honored (runs under `-race`) |

Test logs use a fixed `business=<area> case=<name> … result=<ok|…>` shape so
run output shows which businesses, cases and numerical results were covered.
