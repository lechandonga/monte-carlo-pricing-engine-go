package analytics

import "math"

// BarrierSpec selects one of the eight standard single-barrier contracts.
type BarrierSpec struct {
	Call    bool
	Up      bool
	KnockIn bool
	// Rebate: paid at hit for knock-outs, at expiry if never knocked in for
	// knock-ins (Reiner-Rubinstein convention).
	Rebate float64
}

// Barrier is the Reiner-Rubinstein closed form for a continuously monitored
// single-barrier option. The term definitions and branch table follow Haug,
// "The Complete Guide to Option Pricing Formulas" (the fOptions
// StandardBarrierOption implementation, for which every in/out pair was
// checked against in+out = vanilla with zero rebate). Inputs are assumed to
// have already been validated.
func Barrier(spot, strike, t, r, q, vol, h, rebate float64, spec BarrierSpec) float64 {
	mu := (r - q - vol*vol/2) / (vol * vol)
	lambda := math.Sqrt(mu*mu + 2*r/(vol*vol))
	sigSqrtT := vol * math.Sqrt(t)

	x1 := math.Log(spot/strike)/sigSqrtT + (1+mu)*sigSqrtT
	x2 := math.Log(spot/h)/sigSqrtT + (1+mu)*sigSqrtT
	y1 := math.Log(h*h/(spot*strike))/sigSqrtT + (1+mu)*sigSqrtT
	y2 := math.Log(h/spot)/sigSqrtT + (1+mu)*sigSqrtT
	z := math.Log(h/spot)/sigSqrtT + lambda*sigSqrtT

	// eta: +1 for down barriers, -1 for up. phi: +1 call, -1 put.
	eta, phi := 1.0, 1.0
	if spec.Up {
		eta = -1
	}
	if !spec.Call {
		phi = -1
	}

	dq := math.Exp(-q * t)
	dr := math.Exp(-r * t)
	ratio1 := math.Pow(h/spot, 2*(mu+1))
	ratio2 := math.Pow(h/spot, 2*mu)

	f1 := phi*spot*dq*norm(phi*x1) - phi*strike*dr*norm(phi*x1-phi*sigSqrtT)
	f2 := phi*spot*dq*norm(phi*x2) - phi*strike*dr*norm(phi*x2-phi*sigSqrtT)
	f3 := phi*spot*dq*ratio1*norm(eta*y1) - phi*strike*dr*ratio2*norm(eta*y1-eta*sigSqrtT)
	f4 := phi*spot*dq*ratio1*norm(eta*y2) - phi*strike*dr*ratio2*norm(eta*y2-eta*sigSqrtT)
	// Rebate terms: f5 is the at-expiry payment for knock-ins, f6 the
	// risk-neutral expected at-hit payment for knock-outs.
	f5 := rebate * dr * (norm(eta*x2-eta*sigSqrtT) - ratio2*norm(eta*y2-eta*sigSqrtT))
	f6 := rebate * (math.Pow(h/spot, mu+lambda)*norm(eta*z) +
		math.Pow(h/spot, mu-lambda)*norm(eta*z-2*eta*lambda*sigSqrtT))

	aboveBarrier := strike >= h // fOptions "X >= H" branch selector
	var v float64
	switch {
	case spec.Call && !spec.Up && spec.KnockIn: // cdi
		if aboveBarrier {
			v = f3 + f5
		} else {
			v = f1 - f2 + f4 + f5
		}
	case spec.Call && spec.Up && spec.KnockIn: // cui
		if aboveBarrier {
			v = f1 + f5
		} else {
			v = f2 - f3 + f4 + f5
		}
	case !spec.Call && !spec.Up && spec.KnockIn: // pdi
		if aboveBarrier {
			v = f2 - f3 + f4 + f5
		} else {
			v = f1 + f5
		}
	case !spec.Call && spec.Up && spec.KnockIn: // pui
		if aboveBarrier {
			v = f1 - f2 + f4 + f5
		} else {
			v = f3 + f5
		}
	case spec.Call && !spec.Up && !spec.KnockIn: // cdo
		if aboveBarrier {
			v = f1 - f3 + f6
		} else {
			v = f2 - f4 + f6
		}
	case spec.Call && spec.Up && !spec.KnockIn: // cuo
		if aboveBarrier {
			v = f6
		} else {
			v = f1 - f2 + f3 - f4 + f6
		}
	case !spec.Call && !spec.Up && !spec.KnockIn: // pdo
		if aboveBarrier {
			v = f1 - f2 + f3 - f4 + f6
		} else {
			v = f6
		}
	default: // puo
		if aboveBarrier {
			v = f2 - f4 + f6
		} else {
			v = f1 - f3 + f6
		}
	}
	return v
}

func norm(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
