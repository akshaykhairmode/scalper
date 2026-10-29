package web

import (
	"context"
	"sort"
	"time"

	"github.com/akshaykhairmode/scalper/internal/kotakhttp"
)

// logKotakRate records the actual upstream call rate once a minute. Kotak only
// logs failures today, which makes burst problems invisible until a 429 lands.
func (s *Server) logKotakRate(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			counts, limited, errs := kotakhttp.Report()
			total := 0
			for _, n := range counts {
				total += n
			}
			if total == 0 && limited == 0 {
				continue
			}
			keys := make([]string, 0, len(counts))
			for key := range counts {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			breakdown := make([]any, 0, len(keys)*2)
			for _, key := range keys {
				breakdown = append(breakdown, key, counts[key])
			}
			s.log.Info("Kotak API request rate",
				"event", "kotak_api_rate",
				"requests_per_minute", total,
				"rate_limited", limited,
				"transport_errors", errs,
				"by_endpoint", breakdown,
			)
		}
	}
}
