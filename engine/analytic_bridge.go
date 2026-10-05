package engine

import (
	"github.com/lechandonga/monte-carlo-pricing-engine-go/analytics"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

func analyticsBlackScholes(p option.Params, call bool) float64 {
	return analytics.BlackScholes(p.Spot, p.Strike, p.T, p.R, p.Q, p.Vol, call)
}

func analyticsBarrier(p option.Params, call bool) float64 {
	return analytics.Barrier(p.Spot, p.Strike, p.T, p.R, p.Q, p.Vol, p.Barrier, p.Rebate,
		analytics.BarrierSpec{
			Call:    call,
			Up:      p.Direction == option.Up,
			KnockIn: p.BarType == option.KnockIn,
			Rebate:  p.Rebate,
		})
}
