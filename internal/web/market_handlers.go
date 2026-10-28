package web

import (
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

	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/paper"
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

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot(w, r)
	if !ok {
		return
	}
	for i := range snapshot.Positions {
		position := &snapshot.Positions[i]
		if s.market == nil {
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "live quote source is not configured", nil)
			return
		}
		option, exists := s.market.Option(position.InstrumentKey)
		if !exists {
			s.writeRouteError(w, r, http.StatusServiceUnavailable,
				"paper position is not in the current Scrip Master: "+position.InstrumentKey, nil)
			return
		}
		quote, err := s.market.Quote(r.Context(), s.marketBaseURL(), position.InstrumentKey)
		if err != nil {
			s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
			return
		}
		position.ExchangeSegment = option.ExchangeSegment
		position.Symbol = option.PTrdSymbol
		position.Underlying = option.Underlying
		position.Expiry = option.Expiry
		position.Strike = float64(option.StrikePaise) / 100
		position.OptionType = string(option.OptionType)
		position.LotSize = option.LotSize
		position.LTP = quote.LTP
		position.MarketValue = quote.LTP * float64(position.Quantity)
		position.PnL = (quote.LTP - position.AveragePrice) * float64(position.Quantity)
	}
	s.writeRouteJSON(w, r, http.StatusOK, snapshot.Positions)
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot(w, r)
	if !ok {
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, snapshot.Orders)
}

func (s *Server) handleTrades(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot(w, r)
	if !ok {
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, snapshot.Trades)
}

func (s *Server) handleLimits(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.paperSnapshot(w, r)
	if !ok {
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]float64{
		"total_margin":     snapshot.Account.StartingCash,
		"used_margin":      snapshot.Account.Used,
		"available_margin": snapshot.Account.Available,
		"funds_available":  snapshot.Account.Available,
	})
}

func (s *Server) handleRisk(w http.ResponseWriter, r *http.Request) {
	s.writeRouteError(w, r, http.StatusNotImplemented, "risk payoff view is not implemented", nil)
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

type paperOrderRequest struct {
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Symbol          string  `json:"symbol"`
	Underlying      string  `json:"underlying"`
	Expiry          string  `json:"expiry"`
	Strike          float64 `json:"strike"`
	OptionType      string  `json:"option_type"`
	Side            string  `json:"side"`
	BuySell         string  `json:"buy_sell"`
	Quantity        int64   `json:"quantity"`
	Qty             int64   `json:"qty"`
	Product         string  `json:"product"`
	Intent          string  `json:"intent"`
	Mode            string  `json:"mode"`
	Price           float64 `json:"price"`
	OrderType       string  `json:"order_type"`
	LotSize         int64   `json:"lot_size"`
}

func (s *Server) handlePaperOrder(w http.ResponseWriter, r *http.Request) {
	if s.paper == nil || s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper trading or live quotes are not configured", nil)
		return
	}
	var request paperOrderRequest
	if err := decodeRequest(w, r, &request); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid order request: "+err.Error(), err)
		return
	}
	if request.Mode != "paper" {
		s.writeRouteError(w, r, http.StatusConflict, "only paper orders are supported; no broker order was sent", nil)
		return
	}
	if request.Side == "" {
		request.Side = request.BuySell
	} else if request.BuySell != "" && !strings.EqualFold(request.Side, request.BuySell) {
		s.writeRouteError(w, r, http.StatusBadRequest, "side and buy_sell must match", nil)
		return
	}
	if request.OrderType != "" && request.OrderType != "MARKET" {
		s.writeRouteError(w, r, http.StatusBadRequest, "only MARKET paper orders are supported", nil)
		return
	}
	if request.Quantity == 0 {
		request.Quantity = request.Qty
	}
	option, ok := s.market.Option(request.InstrumentKey)
	if !ok {
		s.writeRouteError(w, r, http.StatusBadRequest, "instrument is not in the current Scrip Master", nil)
		return
	}
	quote, err := s.market.Quote(r.Context(), s.marketBaseURL(), request.InstrumentKey)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	if err := s.requireFreshQuote(quote); err != nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, err.Error(), err)
		return
	}
	product := request.Product
	if product == "" {
		product = s.cfg.Trading.DefaultProduct
	}
	order, err := s.paper.PlaceOrder(r.Context(), paper.OrderRequest{
		Instrument: option,
		Side:       strings.ToUpper(request.Side),
		Quantity:   request.Quantity,
		Product:    product,
		Intent:     request.Intent,
		FillPrice:  quote.LTP,
	})
	if err != nil {
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	s.log.Info("paper order filled at live Kotak quote",
		"event", "paper_order_filled",
		"order_id", order.ID,
		"instrument", order.InstrumentKey,
		"side", order.Side,
		"quantity", order.Quantity,
		"price", order.Price,
		"quote_updated_at", quote.UpdatedAt)
	s.writeRouteJSON(w, r, http.StatusCreated, order)
}

func (s *Server) handleSquareOffAll(w http.ResponseWriter, r *http.Request) {
	if s.paper == nil || s.market == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper trading or live quotes are not configured", nil)
		return
	}
	var request struct {
		Confirm bool   `json:"confirm"`
		Mode    string `json:"mode"`
	}
	if err := decodeRequest(w, r, &request); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid square-off request: "+err.Error(), err)
		return
	}
	if !request.Confirm || request.Mode != "paper" {
		s.writeRouteError(w, r, http.StatusConflict, "confirmed paper square-off is required", nil)
		return
	}
	snapshot, err := s.paper.Snapshot(r.Context())
	if err != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, err.Error(), err)
		return
	}
	filled := make([]paper.Order, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		option, ok := s.market.Option(position.InstrumentKey)
		if !ok {
			s.writeRouteError(w, r, http.StatusServiceUnavailable,
				"paper position is not in the current Scrip Master: "+position.InstrumentKey, nil)
			return
		}
		quote, err := s.market.Quote(r.Context(), s.marketBaseURL(), position.InstrumentKey)
		if err != nil {
			s.writeRouteError(w, r, http.StatusBadGateway,
				fmt.Sprintf("square-off partially complete (%d filled): %v", len(filled), err), err)
			return
		}
		if err := s.requireFreshQuote(quote); err != nil {
			s.writeRouteError(w, r, http.StatusServiceUnavailable,
				fmt.Sprintf("square-off partially complete (%d filled): %v", len(filled), err), err)
			return
		}
		side := "B"
		if position.Quantity > 0 {
			side = "S"
		}
		order, err := s.paper.PlaceOrder(r.Context(), paper.OrderRequest{
			Instrument: option,
			Side:       side,
			Quantity:   absQuantity(position.Quantity),
			Product:    s.cfg.Trading.DefaultProduct,
			Intent:     "EXIT",
			FillPrice:  quote.LTP,
		})
		if err != nil {
			s.writeRouteError(w, r, http.StatusUnprocessableEntity,
				fmt.Sprintf("square-off partially complete (%d filled): %v", len(filled), err), err)
			return
		}
		filled = append(filled, order)
	}
	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"orders": filled, "filled": len(filled)})
}

func (s *Server) paperSnapshot(w http.ResponseWriter, r *http.Request) (paper.Snapshot, bool) {
	if s.paper == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not configured", nil)
		return paper.Snapshot{}, false
	}
	snapshot, err := s.paper.Snapshot(r.Context())
	if err != nil {
		s.writeRouteError(w, r, http.StatusInternalServerError, err.Error(), err)
		return paper.Snapshot{}, false
	}
	return snapshot, true
}

func (s *Server) requireFreshQuote(quote market.Quote) error {
	if quote.UpdatedAt <= 0 || quote.LTP <= 0 || math.IsNaN(quote.LTP) || math.IsInf(quote.LTP, 0) {
		return errors.New("a live Kotak quote with a valid timestamp and positive LTP is required")
	}
	updatedAt := quote.UpdatedAt
	if updatedAt > 1_000_000_000_000 {
		updatedAt /= 1000
	}
	age := s.now().Sub(time.Unix(updatedAt, 0))
	if age < -time.Minute {
		return errors.New("kotak quote timestamp is in the future")
	}
	maxAge := time.Duration(s.cfg.Trading.Risk.BlockOnDataStaleMS) * time.Millisecond
	if maxAge > 0 && age > maxAge {
		return fmt.Errorf("kotak quote is stale (%s old)", age.Round(time.Millisecond))
	}
	return nil
}

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

func absQuantity(quantity int64) int64 {
	if quantity < 0 {
		return -quantity
	}
	return quantity
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
