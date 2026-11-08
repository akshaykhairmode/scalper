package features

import "math"

// CalculateVWAP calculates the Volume Weighted Average Price from historical prices and volumes.
func CalculateVWAP(prices []float64, volumes []int64) float64 {
	if len(prices) == 0 || len(prices) != len(volumes) {
		return 0
	}
	var totalPV float64
	var totalVol int64
	for i := range prices {
		if volumes[i] <= 0 || prices[i] <= 0 {
			continue
		}
		totalPV += prices[i] * float64(volumes[i])
		totalVol += volumes[i]
	}
	if totalVol == 0 {
		return prices[len(prices)-1]
	}
	return totalPV / float64(totalVol)
}

// CalculateEMA computes the Exponential Moving Average of prices over the given period.
func CalculateEMA(prices []float64, period int) float64 {
	if len(prices) == 0 || period <= 0 {
		return 0
	}
	if len(prices) < period {
		// Fallback to simple average if fewer bars than period
		var sum float64
		for _, p := range prices {
			sum += p
		}
		return sum / float64(len(prices))
	}

	multiplier := 2.0 / float64(period+1)

	// Seed with SMA of the first 'period' values
	var sum float64
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	ema := sum / float64(period)

	for i := period; i < len(prices); i++ {
		ema = (prices[i]-ema)*multiplier + ema
	}
	return ema
}

// CalculateRSI computes Wilder's Relative Strength Index over the given period (standard: 14).
func CalculateRSI(closes []float64, period int) float64 {
	if len(closes) <= period || period <= 0 {
		return 50.0 // Neutral default when insufficient bars
	}

	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		change := closes[i] - closes[i-1]
		if change > 0 {
			gainSum += change
		} else {
			lossSum -= change
		}
	}

	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)

	for i := period + 1; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		var gain, loss float64
		if change > 0 {
			gain = change
		} else {
			loss = -change
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}

	if avgLoss == 0 {
		return 100.0
	}
	rs := avgGain / avgLoss
	return 100.0 - (100.0 / (1.0 + rs))
}

// CalculateATR computes the Average True Range over the given period (standard: 14).
func CalculateATR(highs, lows, closes []float64, period int) float64 {
	n := len(closes)
	if n < 2 || len(highs) != n || len(lows) != n || period <= 0 {
		return 0
	}

	tr := make([]float64, n)
	tr[0] = highs[0] - lows[0]
	for i := 1; i < n; i++ {
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		tr[i] = math.Max(hl, math.Max(hc, lc))
	}

	if n < period {
		var sum float64
		for _, v := range tr {
			sum += v
		}
		return sum / float64(n)
	}

	var sum float64
	for i := 0; i < period; i++ {
		sum += tr[i]
	}
	atr := sum / float64(period)

	for i := period; i < n; i++ {
		atr = (atr*float64(period-1) + tr[i]) / float64(period)
	}
	return atr
}
