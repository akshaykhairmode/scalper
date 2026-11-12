package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/errs"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
)

func (s *Server) marketBaseURL() string {
	if session := s.a.CurrentSession(); session != nil && session.BaseURL != "" {
		return session.BaseURL
	}
	return market.DefaultBaseURL
}

// indicesTTL collapses the 10s polls of every open tab into at most one
// upstream quote batch every few seconds.
const indicesTTL = 3 * time.Second

type chainCacheEntry struct {
	payload   []byte
	fetchedAt time.Time
}

const (
	chainTTLOpen   = 5 * time.Second
	chainTTLClosed = 60 * time.Second
)

func (s *Server) handleIndices(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	writeCached := func(payload json.RawMessage) {
		if payload == nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(payload); err != nil {
			s.log.Warn("write cached indices", "event", "http_write_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
		}
	}
	if payload, ok := s.cachedIndicesPayload(); ok {
		writeCached(payload)
		return
	}

	// Serialise cold-cache fetches so simultaneous tabs share one upstream call.
	// The cache lock itself is only held briefly for reads/writes.
	s.indicesFetchMu.Lock()
	if payload, ok := s.cachedIndicesPayload(); ok {
		s.indicesFetchMu.Unlock()
		writeCached(payload)
		return
	}
	result, err := s.market.Indices(r.Context(), s.marketBaseURL())
	if err != nil {
		s.indicesFetchMu.Unlock()
		if payload, ok := s.cachedIndicesAny(); ok {
			s.log.Warn("Kotak indices refresh failed; serving cached indices",
				"event", "indices_refresh_failed",
				"error", err,
				"request_id", logging.RequestID(r.Context()))
			writeCached(payload)
			return
		}
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	payload, encodeErr := json.Marshal(map[string]any{"indices": result})
	if encodeErr == nil {
		s.storeIndicesPayload(payload)
	}
	s.indicesFetchMu.Unlock()
	for _, index := range result {
		if !index.QuoteAvailable {
			s.log.Warn("Kotak did not return a quote for a configured underlying",
				"event", "market_underlying_quote_missing",
				"underlying", index.Key,
				"symbol", index.IndexSymbol,
				"request_id", logging.RequestID(r.Context()))
		}
	}
	if encodeErr != nil {
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"indices": result})
		return
	}
	writeCached(payload)
}

func (s *Server) cachedIndicesPayload() (json.RawMessage, bool) {
	s.indicesMu.RLock()
	defer s.indicesMu.RUnlock()
	if s.indicesRaw != nil && time.Since(s.indicesAt) < indicesTTL {
		return s.indicesRaw, true
	}
	return nil, false
}

func (s *Server) cachedIndicesAny() (json.RawMessage, bool) {
	s.indicesMu.RLock()
	defer s.indicesMu.RUnlock()
	return s.indicesRaw, s.indicesRaw != nil
}

func (s *Server) storeIndicesPayload(payload json.RawMessage) {
	if len(payload) == 0 {
		return
	}
	s.indicesMu.Lock()
	s.indicesRaw = payload
	s.indicesAt = time.Now()
	s.indicesMu.Unlock()
}

func (s *Server) handleExpiries(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "Scrip Master is not configured", nil)
		return
	}
	index := strings.TrimSpace(r.URL.Query().Get("index"))
	if index == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "index is required", nil)
		return
	}
	expiries, err := s.market.Expiries(index)
	if err != nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"expiries": expiries})
}

func (s *Server) handleChain(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	index := strings.TrimSpace(r.URL.Query().Get("index"))
	expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
	if index == "" || expiry == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "index and expiry are required", nil)
		return
	}
	ttl := chainTTLOpen
	if !s.isMarketOpen(s.now()) {
		ttl = chainTTLClosed
	}

	if countRaw := r.URL.Query().Get("count"); countRaw != "" {
		count, err := strconv.Atoi(countRaw)
		if err != nil || count < 1 || count > 20 {
			s.writeRouteError(w, r, http.StatusBadRequest, "count must be between 1 and 20", nil)
			return
		}
		center, err := strconv.ParseFloat(r.URL.Query().Get("center"), 64)
		if err != nil || math.IsNaN(center) || math.IsInf(center, 0) || center < 0 {
			s.writeRouteError(w, r, http.StatusBadRequest, "a non-negative center strike is required", nil)
			return
		}
		direction := strings.TrimSpace(r.URL.Query().Get("direction"))
		if direction == "" {
			direction = "around"
		}
		if direction != "around" && direction != "above" && direction != "below" {
			s.writeRouteError(w, r, http.StatusBadRequest, "direction must be around, above, or below", nil)
			return
		}
		windowAPI, ok := s.market.(chainWindowAPI)
		if !ok {
			s.writeRouteError(w, r, http.StatusNotImplemented, "selective chain loading is unavailable", nil)
			return
		}

		cacheKey := fmt.Sprintf("%s|%s|%.2f|%d|%s", index, expiry, center, count, direction)
		s.chainCacheMu.Lock()
		cached, hasCached := s.chainCache[cacheKey]
		if hasCached && time.Since(cached.fetchedAt) < ttl {
			payload := cached.payload
			s.chainCacheMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		s.chainCacheMu.Unlock()

		page, err := windowAPI.ChainWindow(r.Context(), s.marketBaseURL(), index, expiry, center, count, direction)
		if err != nil {
			if hasCached {
				s.log.Warn("Kotak chain quote refresh failed; serving cached chain",
					"event", "chain_refresh_failed",
					"error", err,
					"index", index,
					"expiry", expiry,
					"request_id", logging.RequestID(r.Context()))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(cached.payload)
				return
			}
			s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
			return
		}
		payload, encodeErr := json.Marshal(page)
		if encodeErr == nil {
			s.storeChainCache(cacheKey, payload)
		}
		s.ingestChainQuotes(page.Rows)
		s.writeRouteJSON(w, r, http.StatusOK, page)
		return
	}

	cacheKey := fmt.Sprintf("%s|%s|full", index, expiry)
	s.chainCacheMu.Lock()
	cached, hasCached := s.chainCache[cacheKey]
	if hasCached && time.Since(cached.fetchedAt) < ttl {
		payload := cached.payload
		s.chainCacheMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	s.chainCacheMu.Unlock()

	rows, err := s.market.Chain(r.Context(), s.marketBaseURL(), index, expiry)
	if err != nil {
		if hasCached {
			s.log.Warn("Kotak full chain refresh failed; serving cached chain",
				"event", "chain_refresh_failed",
				"error", err,
				"index", index,
				"expiry", expiry,
				"request_id", logging.RequestID(r.Context()))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cached.payload)
			return
		}
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	payload, encodeErr := json.Marshal(map[string]any{"rows": rows})
	if encodeErr == nil {
		s.storeChainCache(cacheKey, payload)
	}
	s.ingestChainQuotes(rows)
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"rows": rows})
}

type chainStatsPayload struct {
	MaxPain    float64 `json:"maxPain"`
	CallOIWall float64 `json:"callOIWall"`
	PutOIWall  float64 `json:"putOIWall"`
	PCR        float64 `json:"pcr"`
	Strikes    int     `json:"strikes"`
	AsOf       int64   `json:"asOf"`
}

// handleChainStats returns chain-level levels that need the full strike
// ladder, like max pain and the largest CE/PE open-interest walls. The chart's
// normal windowed chain is intentionally small, so it cannot compute these
// reliably.
func (s *Server) handleChainStats(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	index := strings.TrimSpace(r.URL.Query().Get("index"))
	expiry := strings.TrimSpace(r.URL.Query().Get("expiry"))
	if index == "" || expiry == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "index and expiry are required", nil)
		return
	}

	ttl := 30 * time.Second
	if !s.isMarketOpen(s.now()) {
		ttl = 60 * time.Second
	}
	cacheKey := fmt.Sprintf("%s|%s|stats", index, expiry)
	s.chainCacheMu.Lock()
	cached, hasCached := s.chainCache[cacheKey]
	s.chainCacheMu.Unlock()
	if hasCached && time.Since(cached.fetchedAt) < ttl {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached.payload)
		return
	}

	rows, err := s.market.Chain(r.Context(), s.marketBaseURL(), index, expiry)
	if err != nil {
		if hasCached {
			s.log.Warn("Kotak chain stats refresh failed; serving cached stats",
				"event", "chain_stats_refresh_failed",
				"error", err,
				"index", index,
				"expiry", expiry,
				"request_id", logging.RequestID(r.Context()))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cached.payload)
			return
		}
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}

	var callOI, putOI, callWall, putWall int64
	for _, row := range rows {
		if row.Call != nil {
			callOI += row.Call.OI
			if row.Call.OI > callWall {
				callWall = row.Call.OI
			}
		}
		if row.Put != nil {
			putOI += row.Put.OI
			if row.Put.OI > putWall {
				putWall = row.Put.OI
			}
		}
	}
	stats := chainStatsPayload{
		Strikes: len(rows),
		AsOf:    s.now().UnixMilli(),
	}
	if callOI+putOI > 0 {
		stats.MaxPain = computeMaxPain(rows)
	}
	if callOI > 0 {
		stats.PCR = float64(putOI) / float64(callOI)
	}
	if callWall > 0 || putWall > 0 {
		for _, row := range rows {
			if callWall > 0 && row.Call != nil && row.Call.OI == callWall {
				stats.CallOIWall = row.Strike
			}
			if putWall > 0 && row.Put != nil && row.Put.OI == putWall {
				stats.PutOIWall = row.Strike
			}
		}
	}

	payload, encodeErr := json.Marshal(stats)
	if encodeErr != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, "encode chain stats failed", encodeErr)
		return
	}
	s.storeChainCache(cacheKey, payload)
	s.writeRouteJSON(w, r, http.StatusOK, stats)
}

const maxChainCacheEntries = 128

func (s *Server) storeChainCache(key string, payload []byte) {
	s.chainCacheMu.Lock()
	if len(s.chainCache) >= maxChainCacheEntries {
		for existing := range s.chainCache {
			delete(s.chainCache, existing)
			break
		}
	}
	s.chainCache[key] = chainCacheEntry{payload: payload, fetchedAt: time.Now()}
	s.chainCacheMu.Unlock()
}

type candleFetchResult struct {
	Candles    []market.Candle `json:"candles"`
	Source     string          `json:"source,omitempty"`
	Stale      bool            `json:"stale,omitempty"`
	Warning    string          `json:"warning,omitempty"`
	Error      string          `json:"error,omitempty"`
	StatusCode int             `json:"statusCode,omitempty"`
	RetryAfter string          `json:"retryAfter,omitempty"`
}

func (s *Server) isTimeframeAllowed(timeframe string) bool {
	// 4h is synthesized from Kotak's 60-minute candles, so it remains available
	// even when an older deployed config.yaml predates the built-in timeframe.
	if timeframe == "4h" {
		return true
	}
	for _, configured := range s.configSnapshot().Charts.Timeframes {
		if timeframe == configured {
			return true
		}
	}
	return false
}

func (s *Server) fetchInstrumentCandles(
	ctx context.Context,
	instrument, timeframe, sessionDate string,
	localNow time.Time,
	forceRefresh bool,
) candleFetchResult {
	if instrument == "" {
		return candleFetchResult{}
	}
	// MCX has no broker history, so seed the live bar from the cached quote even
	// when the contract has not traded since the chart was opened.
	if s.candleAgg != nil && strings.HasPrefix(instrument, "mcx_fo|") && s.candleSessionAllows(instrument, localNow) {
		if ltp, ok := s.cachedLTP(instrument, 5*time.Minute); ok {
			s.candleAgg.OnTick(instrument, ltp, 0, 0, localNow)
		}
	}
	var cached []market.Candle
	if s.candleStore != nil {
		var cacheErr error
		cached, cacheErr = s.candleStore.ReadCandles(ctx, instrument, timeframe)
		if cacheErr != nil {
			s.log.Warn("read historical candles from SQLite", "event", "candle_cache_read_failed", "error", cacheErr,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(ctx))
			cached = nil
		}
		cached = s.filterCandles(instrument, cached)
		if len(cached) == 0 && timeframe != "1m" && timeframe != "4h" {
			// Older or tick-only data may exist as 1-minute bars. Derive the
			// requested timeframe so MCX charts can still show history. 4h is
			// aggregated from Kotak's 60-minute bars because its session-aligned
			// buckets cannot be reconstructed from epoch-aligned 1m buckets.
			if synth, synthErr := s.candleStore.AggregateCandlesFrom1m(ctx, instrument, timeframe); synthErr == nil && len(synth) > 0 {
				cached = s.filterCandles(instrument, synth)
			}
		}
		if len(cached) > 0 && isMalformedCandleRun(cached) {
			s.log.Warn("cached candles contain malformed run; repairing from 1m store",
				"event", "malformed_cached_candles", "instrument", instrument, "timeframe", timeframe)
			if timeframe != "1m" && timeframe != "4h" {
				if synth, synthErr := s.candleStore.AggregateCandlesFrom1m(ctx, instrument, timeframe); synthErr == nil && len(synth) > 0 {
					cached = s.filterCandles(instrument, synth)
					if writeErr := s.candleStore.WriteCandles(ctx, instrument, timeframe, cached); writeErr != nil {
						s.log.Warn("failed to write repaired candles to store", "event", "candle_cache_repair_failed",
							"instrument", instrument, "timeframe", timeframe, "error", writeErr)
					}
				}
			}
		}
		syncedAt, synced, syncErr := s.candleStore.CandleSyncAt(ctx, instrument, timeframe, sessionDate)
		if syncErr != nil {
			s.log.Warn("read historical candle sync marker", "event", "candle_sync_read_failed", "error", syncErr,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(ctx))
		}
		session := s.configSnapshot().Trading.Session
		if !forceRefresh && len(cached) > 0 && syncErr == nil && synced && !candleRefreshDue(
			localNow, session.Open, session.Close, syncedAt) {
			return candleFetchResult{Candles: cached, Source: "sqlite"}
		}
	}

	// Kotak has no historical endpoint for MCX. Serve the bars collected from
	// the live feed instead of returning an upstream error.
	if strings.HasPrefix(instrument, "mcx_fo|") {
		if len(cached) > 0 {
			warning := ""
			if len(cached) < 5 {
				warning = "Kotak has no MCX history; bars build from live quotes."
			}
			return candleFetchResult{Candles: cached, Source: "sqlite", Warning: warning}
		}
		return candleFetchResult{
			Candles: []market.Candle{},
			Source:  "sqlite",
			Warning: "Collecting live MCX candles from the feed…",
		}
	}

	fetched, err := s.market.Candles(ctx, s.marketBaseURL(), instrument, timeframe)
	fetched = s.filterCandles(instrument, fetched)
	if err == nil && isMalformedCandleRun(fetched) {
		s.log.Warn("upstream historical candles malformed (clamped day-high/close); rejecting fetched bars",
			"event", "malformed_upstream_candles", "instrument", instrument, "timeframe", timeframe)
		if s.candleStore != nil && timeframe != "1m" && timeframe != "4h" {
			if synth, synthErr := s.candleStore.AggregateCandlesFrom1m(ctx, instrument, timeframe); synthErr == nil && len(synth) > 0 {
				fetched = s.filterCandles(instrument, synth)
			} else {
				fetched = nil
			}
		} else {
			fetched = nil
		}
	}
	if err != nil {
		if len(cached) > 0 {
			s.log.Warn("historical candle refresh failed; using cached bars", "event", "candle_refresh_failed",
				"error", err, "instrument", instrument, "timeframe", timeframe)
			return candleFetchResult{
				Candles: cached,
				Source:  "sqlite",
				Stale:   true,
				Warning: "Kotak historical refresh failed; showing saved candles.",
			}
		}
		status := http.StatusBadGateway
		var upstream *market.HTTPStatusError
		var retryAfter string
		if errors.As(err, &upstream) && upstream.StatusCode >= 400 && upstream.StatusCode <= 599 {
			status = upstream.StatusCode
			retryAfter = upstream.RetryAfter
		}
		return candleFetchResult{
			Error:      err.Error(),
			StatusCode: status,
			RetryAfter: retryAfter,
		}
	}

	if len(fetched) > 0 && s.candleStore != nil && !isMalformedCandleRun(fetched) {
		if err := s.candleStore.WriteCandles(ctx, instrument, timeframe, fetched); err != nil {
			s.log.Warn("save historical candles to SQLite", "event", "candle_cache_write_failed", "error", err,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(ctx))
		} else if err := s.candleStore.MarkCandlesSynced(ctx, instrument, timeframe, sessionDate, localNow.Unix()); err != nil {
			s.log.Warn("save historical candle sync marker", "event", "candle_sync_write_failed", "error", err,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(ctx))
		}
	}

	if len(fetched) == 0 && len(cached) > 0 {
		return candleFetchResult{
			Candles: cached,
			Source:  "sqlite",
			Stale:   true,
			Warning: "Kotak returned no candles; showing saved candles.",
		}
	}
	candles := mergeCandles(cached, fetched)
	return candleFetchResult{Candles: candles, Source: "kotak"}
}

func (s *Server) handleCandles(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	instrument := strings.TrimSpace(r.URL.Query().Get("instrument"))
	timeframe := strings.TrimSpace(r.URL.Query().Get("tf"))
	if instrument == "" || timeframe == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "instrument and tf are required", nil)
		return
	}
	if !s.isTimeframeAllowed(timeframe) {
		s.writeRouteError(w, r, http.StatusBadRequest, "unsupported chart timeframe", nil)
		return
	}
	now := s.now()
	location := s.configSnapshot().Location()
	localNow := now.In(location)
	sessionDate := localNow.Format("2006-01-02")
	forceRefresh := r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1"

	res := s.fetchInstrumentCandles(r.Context(), instrument, timeframe, sessionDate, localNow, forceRefresh)
	if res.Error != "" {
		if res.RetryAfter != "" {
			w.Header().Set("Retry-After", res.RetryAfter)
		}
		status := res.StatusCode
		if status == 0 {
			status = http.StatusBadGateway
		}
		s.writeRouteError(w, r, status, res.Error, nil)
		return
	}
	resp := map[string]any{"candles": res.Candles, "source": res.Source}
	if res.Stale {
		resp["stale"] = true
	}
	if res.Warning != "" {
		resp["warning"] = res.Warning
	}
	s.writeRouteJSON(w, r, http.StatusOK, resp)
}

func (s *Server) handleCandlesBatch(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	timeframe := strings.TrimSpace(r.URL.Query().Get("tf"))
	if timeframe == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "tf is required", nil)
		return
	}
	if !s.isTimeframeAllowed(timeframe) {
		s.writeRouteError(w, r, http.StatusBadRequest, "unsupported chart timeframe", nil)
		return
	}
	now := s.now()
	location := s.configSnapshot().Location()
	localNow := now.In(location)
	sessionDate := localNow.Format("2006-01-02")
	forceRefresh := r.URL.Query().Get("refresh") == "true" || r.URL.Query().Get("refresh") == "1"

	ceKey := strings.TrimSpace(r.URL.Query().Get("ce"))
	spotKey := strings.TrimSpace(r.URL.Query().Get("spot"))
	peKey := strings.TrimSpace(r.URL.Query().Get("pe"))

	rawInstruments := r.URL.Query().Get("instruments")
	var extraInstruments []string
	if rawInstruments != "" {
		for _, inst := range strings.Split(rawInstruments, ",") {
			inst = strings.TrimSpace(inst)
			if inst != "" {
				extraInstruments = append(extraInstruments, inst)
			}
		}
	}

	response := make(map[string]any)
	results := make(map[string]candleFetchResult)

	fetchAndRecord := func(key string) candleFetchResult {
		if key == "" {
			return candleFetchResult{}
		}
		if res, ok := results[key]; ok {
			return res
		}
		res := s.fetchInstrumentCandles(r.Context(), key, timeframe, sessionDate, localNow, forceRefresh)
		results[key] = res
		return res
	}

	// Fetch in optimal order: spot first (instant SQLite lookup), then ce, then pe
	if spotKey != "" {
		response["spot"] = fetchAndRecord(spotKey)
	}
	if ceKey != "" {
		response["ce"] = fetchAndRecord(ceKey)
	}
	if peKey != "" {
		response["pe"] = fetchAndRecord(peKey)
	}
	// Only extra instruments (beyond spot/ce/pe) are returned under "results";
	// duplicating the named panes doubled the JSON payload for every chart poll.
	if len(extraInstruments) > 0 {
		extra := make(map[string]candleFetchResult, len(extraInstruments))
		for _, inst := range extraInstruments {
			extra[inst] = fetchAndRecord(inst)
		}
		response["results"] = extra
	}
	s.writeRouteJSON(w, r, http.StatusOK, response)
}

func candleRefreshDue(now time.Time, openText, closeText string, syncedAt int64) bool {
	openTime, openErr := time.Parse("15:04", openText)
	closeTime, err := time.Parse("15:04", closeText)
	if openErr != nil || err != nil {
		return false
	}
	openAt := time.Date(now.Year(), now.Month(), now.Day(), openTime.Hour(), openTime.Minute(), 0, 0, now.Location())
	closeAt := time.Date(now.Year(), now.Month(), now.Day(), closeTime.Hour(), closeTime.Minute(), 0, 0, now.Location())
	if !now.Before(closeAt) {
		return syncedAt < closeAt.Unix()
	}
	if !now.Before(openAt) {
		return syncedAt < openAt.Unix() || now.Unix()-syncedAt > int64(5*time.Minute/time.Second)
	}
	return false
}

func mergeCandles(cached, fetched []market.Candle) []market.Candle {
	byTime := make(map[int64]market.Candle, len(cached)+len(fetched))
	for _, candle := range cached {
		if isValidCandle(candle) {
			byTime[candle.Time] = candle
		}
	}
	for _, candle := range fetched {
		if isValidCandle(candle) {
			byTime[candle.Time] = candle
		}
	}
	out := make([]market.Candle, 0, len(byTime))
	for _, candle := range byTime {
		out = append(out, candle)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
}

func isValidCandle(c market.Candle) bool {
	return c.Time > 0 && c.Open > 0 && c.High > 0 && c.Low > 0 && c.Close > 0 && c.High >= c.Low
}

func (s *Server) writeRouteJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	if err := writeJSON(w, status, value); err != nil {
		s.log.Warn("write market API response", "event", "http_write_failed",
			"error", err, "request_id", logging.RequestID(r.Context()))
	}
}

func (s *Server) writeRouteError(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	if errors.Is(err, errs.ErrRateLimited) {
		status = http.StatusTooManyRequests
	}
	if err != nil {
		s.log.Error("market API request failed", "event", "market_api_failed",
			"path", r.URL.Path, "error", err, "request_id", logging.RequestID(r.Context()))
	}
	if writeErr := writeJSONError(w, status, message, logging.RequestID(r.Context())); writeErr != nil {
		s.log.Warn("write market API error response", "event", "http_write_failed",
			"error", writeErr, "request_id", logging.RequestID(r.Context()))
	}
}

// decodeRequest decodes one bounded JSON value and rejects unknown fields or trailing data.
func decodeRequest(w http.ResponseWriter, r *http.Request, target any) error {
	if !requireJSONContentType(r) {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON value")
		}
		return err
	}
	return nil
}

// isMalformedCandleRun detects upstream provider anomalies such as non-positive prices (e.g. low=0)
// or multiple consecutive bars clamped to static day-high and day-close values (barcode pattern).
func isMalformedCandleRun(candles []market.Candle) bool {
	if len(candles) == 0 {
		return false
	}
	for _, c := range candles {
		if c.Low <= 0 || c.Open <= 0 || c.High <= 0 || c.Close <= 0 || c.High < c.Low {
			return true
		}
	}
	if len(candles) < 5 {
		return false
	}
	consecutive := 1
	for i := 1; i < len(candles); i++ {
		prev := candles[i-1]
		curr := candles[i]
		if curr.High == prev.High && curr.Close == prev.Close && math.Abs(curr.High-curr.Open) > 2.0 {
			consecutive++
			if consecutive >= 5 {
				return true
			}
		} else {
			consecutive = 1
		}
	}
	return false
}
