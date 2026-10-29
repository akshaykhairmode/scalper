package kotakhttp

import (
	"context"
	"math"
	"sync"
	"time"
)

// Limits applied to every Kotak REST request (market data, reports and
// order writes). They mirror the defaults shipped with Kotak's official
// Python SDK and stay comfortably below its per-second ceiling.
const (
	perSecondLimit = 8
	perMinuteLimit = 240
	perHourLimit   = 4000
)

var defaultLimiter = newLimiter(perSecondLimit, perMinuteLimit, perHourLimit)

// bucket is a token bucket. It is safe for concurrent use.
type bucket struct {
	mu       sync.Mutex
	capacity float64
	rate     float64 // tokens per second
	tokens   float64
	last     time.Time
}

func newBucket(capacity, perSecond float64) *bucket {
	return &bucket{
		capacity: capacity,
		rate:     perSecond,
		tokens:   capacity,
		last:     time.Now(),
	}
}

func (b *bucket) take(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		now := time.Now()
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens = math.Min(b.capacity, b.tokens+elapsed*b.rate)
			b.last = now
		}
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
		b.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type limiter struct {
	second *bucket
	minute *bucket
	hour   *bucket
}

func newLimiter(perSecond, perMinute, perHour int) *limiter {
	return &limiter{
		second: newBucket(float64(perSecond), float64(perSecond)),
		minute: newBucket(float64(perMinute), float64(perMinute)/60),
		hour:   newBucket(float64(perHour), float64(perHour)/3600),
	}
}

func (l *limiter) wait(ctx context.Context) error {
	for _, b := range []*bucket{l.second, l.minute, l.hour} {
		if err := b.take(ctx); err != nil {
			return err
		}
	}
	return nil
}
