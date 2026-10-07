package mcengine

import "math"

// Greeks holds bump-and-revalue sensitivities. All estimates use
// common random numbers (the same seed for bumped and base runs), so
// repeated calls with identical inputs return identical values and
// the simulation noise largely cancels between runs.
type Greeks struct {
	Delta float64
	Gamma float64
	Vega  float64 // per 1.00 absolute vol move (i.e. +100 vol points)
	Theta float64 // per year
	Rho   float64 // per 1.00 absolute rate move
}

// Greeks computes central-difference sensitivities for one option.
func (e *Engine) Greeks(opt Option, cfg Config) (Greeks, error) {
	if err := opt.Validate(); err != nil {
		return Greeks{}, err
	}
	dS := opt.Spot * 0.01
	dV := 0.01
	dT := math.Max(opt.Maturity*0.01, 1.0/365.0)
	dR := 0.01

	price := func(o Option) (float64, error) {
		r, err := e.Price(o, cfg)
		if err != nil {
			return 0, err
		}
		return r.Price, nil
	}

	up, dn := opt, opt
	up.Spot, dn.Spot = opt.Spot+dS, opt.Spot-dS
	pUp, err := price(up)
	if err != nil {
		return Greeks{}, err
	}
	pDn, err := price(dn)
	if err != nil {
		return Greeks{}, err
	}
	base, err := price(opt)
	if err != nil {
		return Greeks{}, err
	}

	vUp, vDn := opt, opt
	vUp.Vol, vDn.Vol = opt.Vol+dV, opt.Vol-dV
	if vDn.Vol < 0 {
		vDn.Vol = 0
	}
	pVUp, err := price(vUp)
	if err != nil {
		return Greeks{}, err
	}
	pVDn, err := price(vDn)
	if err != nil {
		return Greeks{}, err
	}

	tUp, tDn := opt, opt
	tUp.Maturity = opt.Maturity + dT
	tDn.Maturity = opt.Maturity - dT
	if tDn.Maturity <= 0 {
		tDn.Maturity = opt.Maturity / 2
	}
	// Rescale any explicit calendar so it stays valid under the
	// maturity bump (must still end exactly at maturity).
	rescale := func(o *Option, m float64) {
		if len(o.ObservationDates) == 0 {
			return
		}
		dates := make([]float64, len(o.ObservationDates))
		f := m / opt.Maturity
		for i, d := range o.ObservationDates {
			dates[i] = d * f
		}
		dates[len(dates)-1] = m
		o.ObservationDates = dates
	}
	rescale(&tUp, tUp.Maturity)
	rescale(&tDn, tDn.Maturity)
	pTUp, err := price(tUp)
	if err != nil {
		return Greeks{}, err
	}
	pTDn, err := price(tDn)
	if err != nil {
		return Greeks{}, err
	}

	rUp, rDn := opt, opt
	rUp.Rate, rDn.Rate = opt.Rate+dR, opt.Rate-dR
	pRUp, err := price(rUp)
	if err != nil {
		return Greeks{}, err
	}
	pRDn, err := price(rDn)
	if err != nil {
		return Greeks{}, err
	}

	return Greeks{
		Delta: (pUp - pDn) / (2 * dS),
		Gamma: (pUp - 2*base + pDn) / (dS * dS),
		Vega:  (pVUp - pVDn) / (vUp.Vol - vDn.Vol),
		Theta: (pTUp - pTDn) / (tUp.Maturity - tDn.Maturity),
		Rho:   (pRUp - pRDn) / (2 * dR),
	}, nil
}
