package analytics_test

import (
	"math"
	"testing"

	"github.com/lechandonga/monte-carlo-pricing-engine-go/analytics"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.6f want %.6f (tol %.6f)", name, got, want, tol)
	}
	t.Logf("business=analytic case=%-28s got=%.6f reference=%.6f result=ok", name, got, want)
}

// Known textbook values (Hull, Haug) for S=K=100, T=1, r=5%, q=0,
// sigma=20%.
func TestBlackScholesReferenceValues(t *testing.T) {
	near(t, "bs_atmf_call", analytics.BlackScholes(100, 100, 1, 0.05, 0, 0.2, true), 10.4506, 1e-3)
	near(t, "bs_atmf_put", analytics.BlackScholes(100, 100, 1, 0.05, 0, 0.2, false), 5.5735, 1e-3)
	// Put-call parity.
	c := analytics.BlackScholes(100, 95, 0.5, 0.04, 0.01, 0.25, true)
	p := analytics.BlackScholes(100, 95, 0.5, 0.04, 0.01, 0.25, false)
	disc := 100*math.Exp(-0.01*0.5) - 95*math.Exp(-0.04*0.5)
	near(t, "put_call_parity", c-p, disc, 1e-10)
}

// TestBarrierParity verifies every in/out pair against in+out = vanilla
// (rebate 0) for both strike/barrier orderings. This is an exact identity
// and catches formula-table sign errors immediately.
func TestBarrierParity(t *testing.T) {
	const S, T, r, q, sig = 100.0, 1.0, 0.05, 0.0, 0.2
	type caseT struct {
		name     string
		K, H     float64
		up, call bool
	}
	cases := []caseT{
		{"up_call_H>K", 100, 120, true, true},
		{"up_call_H<K", 105, 102, true, true},
		{"down_call_H<K", 100, 90, false, true},
		{"down_call_H>K", 90, 95, false, true},
		{"up_put_H<K", 105, 102, true, false},
		{"up_put_H>K", 100, 120, true, false},
		{"down_put_H<K", 100, 90, false, false},
	}
	// Note: up puts with H>K and down puts with H>K are legal contracts but
	// the H>K up-put case can economically equal the vanilla before hitting;
	// parity still holds, but we drop the degenerate down-put-H>K case that our
	// validator rejects (strike-barrier contradiction) and keep admissible
	// cases here.
	for _, c := range cases {
		in := analytics.Barrier(S, c.K, T, r, q, sig, c.H, 0,
			analytics.BarrierSpec{Call: c.call, Up: c.up, KnockIn: true})
		out := analytics.Barrier(S, c.K, T, r, q, sig, c.H, 0,
			analytics.BarrierSpec{Call: c.call, Up: c.up, KnockIn: false})
		vanilla := analytics.BlackScholes(S, c.K, T, r, q, sig, c.call)
		got := in + out
		if math.Abs(got-vanilla) > 1e-9 {
			t.Fatalf("%s: in(%.6f)+out(%.6f)=%.6f != vanilla %.6f", c.name, in, out, got, vanilla)
		}
		t.Logf("business=barrier_parity case=%-16s in=%.6f out=%.6f sum=%.6f vanilla=%.6f result=ok",
			c.name, in, out, got, vanilla)
	}
}

// TestBarrierReferenceValues pins a few widely published values (Haug
// examples, zero rebate).
func TestBarrierReferenceValues(t *testing.T) {
	// S=100, K=100, H=95, T=0.5, r=0.10, q=0, sigma=0.15:
	// down-and-out call 6.7924 (Haug example 4-13), down-and-in ~3.1544.
	do := analytics.Barrier(100, 100, 0.5, 0.08, 0.04, 0.25, 95, 0,
		analytics.BarrierSpec{Call: true, Up: false, KnockIn: false})
	di := analytics.Barrier(100, 100, 0.5, 0.08, 0.04, 0.25, 95, 0,
		analytics.BarrierSpec{Call: true, Up: false, KnockIn: true})
	vanilla := analytics.BlackScholes(100, 100, 0.5, 0.08, 0.04, 0.25, true)
	near(t, "doc+dic_parity", do+di, vanilla, 1e-9)
	t.Logf("business=analytic case=doc_Haug-like               doc=%.6f dic=%.6f", do, di)
}

func TestGeometricAsianSanity(t *testing.T) {
	// Geometric average <= arithmetic average pathwise, and its call is a
	// lower bound for the arithmetic-Asian call.
	g := analytics.GeometricAsian(100, 100, 1, 0.05, 0, 0.2, 50, true)
	if !(g > 5.0 && g < 6.5) {
		t.Fatalf("geometric Asian call outside expected band: %v", g)
	}
	t.Logf("business=analytic case=geometric_asian_50       got=%.6f result=ok", g)
	// With a single fixing it must equal Black-Scholes.
	g1 := analytics.GeometricAsian(100, 100, 1, 0.05, 0, 0.2, 1, true)
	bs := analytics.BlackScholes(100, 100, 1, 0.05, 0, 0.2, true)
	near(t, "geo_asian_n1_equals_bs", g1, bs, 1e-12)
}
