package web

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
)

func (s *Server) marketBaseURL() string {
	if session := s.a.CurrentSession(); session != nil && session.BaseURL != "" {
		return session.BaseURL
	}
	return market.DefaultBaseURL
}

func (s *Server) handleIndices(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "live market data is not configured", nil)
		return
	}
	result, err := s.market.Indices(r.Context(), s.marketBaseURL())
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
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
	allowed := false
	for _, configured := range s.cfg.Charts.Timeframes {
		if timeframe == configured {
			allowed = true
			break
		}
	}
	if !allowed {
		s.writeRouteError(w, r, http.StatusBadRequest, "unsupported chart timeframe", nil)
		return
	}
	now := s.now()
	location := s.cfg.Location()
	localNow := now.In(location)
	sessionDate := localNow.Format("2006-01-02")
	var cached []market.Candle
	if s.candleStore != nil {
		var cacheErr error
		cached, cacheErr = s.candleStore.ReadCandles(r.Context(), instrument, timeframe)
		if cacheErr != nil {
			s.log.Warn("read historical candles from SQLite", "event", "candle_cache_read_failed", "error", cacheErr,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(r.Context()))
			cached = nil
		}
		syncedAt, synced, syncErr := s.candleStore.CandleSyncAt(r.Context(), instrument, timeframe, sessionDate)
		if syncErr != nil {
			s.log.Warn("read historical candle sync marker", "event", "candle_sync_read_failed", "error", syncErr,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(r.Context()))
		}
		if len(cached) > 0 && syncErr == nil && synced && !candleRefreshDue(
			localNow, s.cfg.Trading.Session.Open, s.cfg.Trading.Session.Close, syncedAt) {
			s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"candles": cached, "source": "sqlite"})
			return
		}
	}

	fetched, err := s.market.Candles(r.Context(), s.marketBaseURL(), instrument, timeframe)
	if err != nil {
		if len(cached) > 0 {
			s.log.Warn("historical candle refresh failed; using cached bars", "event", "candle_refresh_failed",
				"error", err, "instrument", instrument, "timeframe", timeframe)
			s.writeRouteJSON(w, r, http.StatusOK, map[string]any{
				"candles": cached, "source": "sqlite", "stale": true,
				"warning": "Kotak historical refresh failed; showing saved candles.",
			})
			return
		}
		status := http.StatusBadGateway
		var upstream *market.HTTPStatusError
		if errors.As(err, &upstream) && upstream.StatusCode >= 400 && upstream.StatusCode <= 599 {
			status = upstream.StatusCode
			if upstream.RetryAfter != "" {
				w.Header().Set("Retry-After", upstream.RetryAfter)
			}
		}
		s.writeRouteError(w, r, status, err.Error(), err)
		return
	}

	if s.candleStore != nil {
		if err := s.candleStore.WriteCandles(r.Context(), instrument, timeframe, fetched); err != nil {
			s.log.Warn("save historical candles to SQLite", "event", "candle_cache_write_failed", "error", err,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(r.Context()))
		} else if err := s.candleStore.MarkCandlesSynced(r.Context(), instrument, timeframe, sessionDate, localNow.Unix()); err != nil {
			s.log.Warn("save historical candle sync marker", "event", "candle_sync_write_failed", "error", err,
				"instrument", instrument, "timeframe", timeframe, "request_id", logging.RequestID(r.Context()))
		}
	}

	if len(fetched) == 0 && len(cached) > 0 {
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{
			"candles": cached, "source": "sqlite", "stale": true,
			"warning": "Kotak returned no candles; showing saved candles.",
		})
		return
	}
	candles := mergeCandles(cached, fetched)
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"candles": candles, "source": "kotak"})
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
