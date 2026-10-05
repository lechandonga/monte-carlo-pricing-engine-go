package engine_test

import (
	"github.com/lechandonga/monte-carlo-pricing-engine-go/analytics"
	"github.com/lechandonga/monte-carlo-pricing-engine-go/option"
)

type analyticG struct{ delta, gamma, vega, rho, theta float64 }

func analyticEuropeanGreeks(p option.Params) analyticG {
	g := analytics.BlackScholesGreeks(p.Spot, p.Strike, p.T, p.R, p.Q, p.Vol, p.Right == option.Call)
	return analyticG{g.Delta, g.Gamma, g.Vega, g.Rho, g.Theta}
}
