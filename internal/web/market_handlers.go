package web

import (
	"context"
	"encoding/json"
	"errors"
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

func (s *Server) handleIndices(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	// Serialise cold-cache calls so simultaneous tabs share one upstream call.
	s.indicesMu.Lock()
	if s.indicesRaw != nil && time.Since(s.indicesAt) < indicesTTL {
		payload := s.indicesRaw
		s.indicesMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(payload); err != nil {
			s.log.Warn("write cached indices", "event", "http_write_failed", "error", err,
				"request_id", logging.RequestID(r.Context()))
		}
		return
	}
	result, err := s.market.Indices(r.Context(), s.marketBaseURL())
	if err != nil {
		s.indicesMu.Unlock()
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	payload, encodeErr := json.Marshal(map[string]any{"indices": result})
	if encodeErr == nil {
		s.indicesRaw = payload
		s.indicesAt = time.Now()
	}
	s.indicesMu.Unlock()
	for _, index := range result {
		if !index.QuoteAvailable {
			s.log.Warn("Kotak did not return a quote for a configured underlying",
				"event", "market_underlying_quote_missing",
				"underlying", index.Key,
				"symbol", index.IndexSymbol,
				"request_id", logging.RequestID(r.Context()))
		}
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"indices": result})
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
		page, err := windowAPI.ChainWindow(r.Context(), s.marketBaseURL(), index, expiry, center, count, direction)
		if err != nil {
			s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
			return
		}
		s.writeRouteJSON(w, r, http.StatusOK, page)
		return
	}
	rows, err := s.market.Chain(r.Context(), s.marketBaseURL(), index, expiry)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"rows": rows})
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
	for _, configured := range s.cfg.Charts.Timeframes {
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
	var cached []market.Candle
	if s.candleStore != nil {
		var cacheErr error
		cached, cacheErr = s.candleStore.ReadCandles(ctx, instrument, timeframe)
		if cacheErr != nil {
			s.log.Warn("read historical candles from SQLite", "event", "candle_cache_read_failed", "error", cacheErr,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(ctx))
			cached = nil
		}
		if len(cached) > 0 && isMalformedCandleRun(cached) {
			s.log.Warn("cached candles contain malformed run; repairing from 1m store",
				"event", "malformed_cached_candles", "instrument", instrument, "timeframe", timeframe)
			if timeframe != "1m" {
				if synth, synthErr := s.candleStore.AggregateCandlesFrom1m(ctx, instrument, timeframe); synthErr == nil && len(synth) > 0 {
					cached = synth
					if writeErr := s.candleStore.WriteCandles(ctx, instrument, timeframe, synth); writeErr != nil {
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
		if !forceRefresh && len(cached) > 0 && syncErr == nil && synced && !candleRefreshDue(
			localNow, s.cfg.Trading.Session.Open, s.cfg.Trading.Session.Close, syncedAt) {
			return candleFetchResult{Candles: cached, Source: "sqlite"}
		}
	}

	fetched, err := s.market.Candles(ctx, s.marketBaseURL(), instrument, timeframe)
	if err == nil && isMalformedCandleRun(fetched) {
		s.log.Warn("upstream historical candles malformed (clamped day-high/close); rejecting fetched bars",
			"event", "malformed_upstream_candles", "instrument", instrument, "timeframe", timeframe)
		if s.candleStore != nil && timeframe != "1m" {
			if synth, synthErr := s.candleStore.AggregateCandlesFrom1m(ctx, instrument, timeframe); synthErr == nil && len(synth) > 0 {
				fetched = synth
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
	location := s.cfg.Location()
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
	location := s.cfg.Location()
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
	for _, inst := range extraInstruments {
		fetchAndRecord(inst)
	}

	response["results"] = results
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
		byTime[candle.Time] = candle
	}
	for _, candle := range fetched {
		byTime[candle.Time] = candle
	}
	out := make([]market.Candle, 0, len(byTime))
	for _, candle := range byTime {
		out = append(out, candle)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out
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

// isMalformedCandleRun detects upstream provider anomalies where multiple consecutive bars
// are clamped to static day-high and day-close values (e.g. barcode pattern).
func isMalformedCandleRun(candles []market.Candle) bool {
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
