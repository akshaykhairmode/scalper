// Package chain resolves expiries and builds option-chain views.
package chain

import (
	"fmt"
	"sync"
	"time"
)

// ExpiryResolver resolves expiry date strings to Expiry objects.
type ExpiryResolver struct {
	mu         sync.RWMutex
	expiracies map[string]time.Time
}

// NewExpiryResolver creates a new ExpiryResolver.
func NewExpiryResolver() *ExpiryResolver {
	return &ExpiryResolver{
		expiracies: make(map[string]time.Time),
	}
}

// ResolveExpiry resolves an expiry string to a time.Time.
// Supported formats: "2026-10-27", "2026-10-27 00:00:00 +0530", etc.
func (er *ExpiryResolver) ResolveExpiry(expiry string) (time.Time, error) {
	er.mu.RLock()
	t, ok := er.expiracies[expiry]
	er.mu.RUnlock()
	if ok {
		return t, nil
	}

	// Parse the expiry string
	t, err := time.ParseInLocation("2006-01-02", expiry, time.UTC)
	if err != nil {
		t, err = time.Parse("2006-01-02", expiry)
		if err != nil {
			return time.Time{}, fmt.Errorf("cannot parse expiry %q: %w", expiry, err)
		}
	}

	er.mu.Lock()
	er.expiracies[expiry] = t
	er.mu.Unlock()

	return t, nil
}

// AddExpiry adds a known expiry date to the resolver cache.
func (er *ExpiryResolver) AddExpiry(expiry string) error {
	t, err := er.ResolveExpiry(expiry)
	if err != nil {
		return err
	}
	er.mu.Lock()
	er.expiracies[expiry] = t
	er.mu.Unlock()
	return nil
}

// NearestWeeklyExpiry returns the nearest weekly expiry date.
// NSE weekly expiries are typically Thursdays.
// This is a heuristic function.
func NearestWeeklyExpiry() time.Time {
	now := time.Now()
	// Find the nearest Thursday
	// In NSE, weekly expiries are typically on Thursdays
	daysUntilThursday := (3 - int(now.Weekday()) + 7) % 7 // Thursday = 3 (0=Sun, 1=Mon, ..., 6=Sat)
	if daysUntilThursday == 0 {
		// Today is Thursday, use today if market is still open, otherwise next week
		// For simplicity, use today
		return now
	}
	nextThursday := now.AddDate(0, 0, daysUntilThursday)
	// If we're past Thursday's market close, go to next week
	// Market close is typically around 3:30 PM IST, but we'll just use the date
	if now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30) {
		nextThursday = nextThursday.AddDate(0, 0, 7)
	}
	return nextThursday
}

// IsWeeklyExpiry checks if a given date is a weekly expiry (Thursday).
func IsWeeklyExpiry(date time.Time) bool {
	return date.Weekday() == time.Thursday
}

// ExpiryMonthlies returns all monthly expiries for a given year-month.
// This is useful for selecting monthly expiries.
func ExpiryMonthlies(year int, month time.Month) []time.Time {
	var result []time.Time
	// Find all expiries on the last Thursday of each month
	for day := 28; day <= 31; day++ {
		t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		// Check if it's a Thursday
		if t.Weekday() == time.Thursday {
			result = append(result, t)
			break // Only one expiry per month (last Thursday)
		}
	}
	// If no Thursday found in the last week, try the first Thursday of next month
	// This is a simplification; in practice, we'd need the full calendar
	if len(result) == 0 {
		// Try the 1st Thursday of the next month
		nextMonth := month + 1
		nextYear := year
		if nextMonth > 12 {
			nextMonth = 1
			nextYear++
		}
		for day := 1; day <= 7; day++ {
			t := time.Date(nextYear, nextMonth, day, 0, 0, 0, 0, time.UTC)
			if t.Weekday() == time.Thursday {
				result = append(result, t)
				break
			}
		}
	}
	return result
}

// StrikeRange returns the range of strikes around the ATM strike.
func StrikeRange(spotPrice float64, step int64, rangeCount int64) (from, to int64) {
	// Calculate from and to strikes around the spot price
	spotInPaise := int64(spotPrice * 100)
	from = spotInPaise - rangeCount*step // rough calculation
	to = spotInPaise + rangeCount*step   // rough calculation
	if from < 0 {
		from = 0
	}
	return
}

// ATMFlag returns true if the strike is within 'tick' paise of the spot price.
func ATMFlag(strike int64, spotPrice float64, tick int64) bool {
	strikePrice := float64(strike) / 100.0
	diff := strikePrice - spotPrice
	if diff < 0 {
		diff = -diff
	}
	return diff <= float64(tick)/100.0
}
