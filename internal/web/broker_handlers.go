package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

func (s *Server) brokerSession(w http.ResponseWriter, r *http.Request) (kotakapi.Session, bool) {
	if s.kotak == nil {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "Kotak trading API is not configured", nil)
		return kotakapi.Session{}, false
	}
	a := s.a.CurrentSession()
	if a == nil || a.BrokerToken == "" || a.BrokerSID == "" {
		s.writeRouteError(w, r, http.StatusUnauthorized, "sign in with Kotak MPIN to use broker APIs", nil)
		return kotakapi.Session{}, false
	}
	return kotakapi.Session{BaseURL: a.BaseURL, SID: a.BrokerSID, Token: a.BrokerToken, ConsumerKey: s.consumerKey}, true
}
func (s *Server) brokerRead(w http.ResponseWriter, r *http.Request, call func(kotakapi.Session) (json.RawMessage, error)) {
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	raw, err := call(session)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, raw)
}
func (s *Server) handleBrokerOrders(w http.ResponseWriter, r *http.Request) {
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookOrders, x)
	})
}
func (s *Server) handleBrokerOrder(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "order id is required", nil)
		return
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) { return s.kotak.Order(r.Context(), x, id) })
}
func (s *Server) handleBrokerTrades(w http.ResponseWriter, r *http.Request) {
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookTrades, x)
	})
}
func (s *Server) handleBrokerPositions(w http.ResponseWriter, r *http.Request) {
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	raw, err := s.getBook(r.Context(), bookPositions, session)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	s.writeRouteJSON(w, r, http.StatusOK, raw)
}

func (s *Server) enrichBrokerPositions(ctx context.Context, raw json.RawMessage) json.RawMessage {
	if s.market == nil {
		return raw
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return raw
	}
	var rows []json.RawMessage
	if json.Unmarshal(envelope["data"], &rows) != nil {
		return raw
	}
	options := make(map[string]struct{}, len(rows))
	keys := make([]string, 0, len(rows))
	for _, encoded := range rows {
		var row map[string]json.RawMessage
		if json.Unmarshal(encoded, &row) != nil {
			continue
		}
		option, found := s.market.FindOption(jsonString(row["exSeg"]), jsonString(row["trdSym"]))
		if !found {
			continue
		}
		if _, seen := options[option.InstrumentKey]; !seen {
			options[option.InstrumentKey] = struct{}{}
			keys = append(keys, option.InstrumentKey)
		}
	}
	// One batched quote request for the whole position book beats one call per
	// position, which used to multiply report polling into the rate limit.
	quotes, quoteErr := s.market.Quotes(ctx, s.marketBaseURL(), keys)
	if quoteErr != nil {
		quotes = nil
	}
	for i, encoded := range rows {
		var row map[string]json.RawMessage
		if json.Unmarshal(encoded, &row) != nil {
			continue
		}
		segment, symbol := jsonString(row["exSeg"]), jsonString(row["trdSym"])
		option, found := s.market.FindOption(segment, symbol)
		if !found {
			continue
		}
		var mErr error
		if row["instrument_key"], mErr = json.Marshal(option.InstrumentKey); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal instrument_key", "error", mErr)
		}
		if row["exchange_segment"], mErr = json.Marshal(option.ExchangeSegment); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal exchange_segment", "error", mErr)
		}
		if row["symbol"], mErr = json.Marshal(option.PTrdSymbol); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal symbol", "error", mErr)
		}
		if row["underlying"], mErr = json.Marshal(option.Underlying); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal underlying", "error", mErr)
		}
		if row["expiry"], mErr = json.Marshal(option.Expiry); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal expiry", "error", mErr)
		}
		if row["strike"], mErr = json.Marshal(float64(option.StrikePaise) / 100); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal strike", "error", mErr)
		}
		if row["option_type"], mErr = json.Marshal(string(option.OptionType)); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal option_type", "error", mErr)
		}
		if row["lot_size"], mErr = json.Marshal(option.LotSize); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal lot_size", "error", mErr)
		}
		if quote, ok := quotes[option.InstrumentKey]; ok && quote.LTP > 0 {
			if row["ltp"], mErr = json.Marshal(quote.LTP); mErr != nil && s.log != nil {
				s.log.Debug("failed to marshal ltp", "error", mErr)
			}
		}
		if rows[i], mErr = json.Marshal(row); mErr != nil && s.log != nil {
			s.log.Debug("failed to marshal position row", "error", mErr)
		}
	}
	if dataBytes, mErr := json.Marshal(rows); mErr == nil {
		envelope["data"] = dataBytes
	} else if s.log != nil {
		s.log.Debug("failed to marshal envelope data", "error", mErr)
	}
	result, err := json.Marshal(envelope)
	if err != nil {
		return raw
	}
	return result
}

func jsonString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func (s *Server) handleBrokerHoldings(w http.ResponseWriter, r *http.Request) {
	if s.books != nil && (r.URL.Query().Get("fresh") == "true" || r.URL.Query().Get("fresh") == "1") {
		s.books.expire(bookHoldings)
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookHoldings, x)
	})
}
func (s *Server) handleBrokerLimits(w http.ResponseWriter, r *http.Request) {
	if s.books != nil && (r.URL.Query().Get("fresh") == "true" || r.URL.Query().Get("fresh") == "1") {
		s.books.expire(bookLimits)
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookLimits, x)
	})
}
func (s *Server) handleBrokerOrderHistory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "order id is required", nil)
		return
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) { return s.kotak.OrderHistory(r.Context(), x, id) })
}

type liveOrderRequest struct {
	kotakapi.Order
	Confirm bool `json:"confirm"`
}

func (s *Server) decodeLiveOrder(w http.ResponseWriter, r *http.Request) (liveOrderRequest, bool) {
	var req liveOrderRequest
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid broker order request: "+err.Error(), err)
		return req, false
	}
	if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
		return req, false
	}
	return req, true
}
func (s *Server) handleBrokerPlaceOrder(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeLiveOrder(w, r)
	if !ok {
		return
	}
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	if err := s.checkOrderRisk(r.Context(), session, req.Order); err != nil {
		s.writeRiskError(w, r, err)
		return
	}
	raw, err := s.kotak.PlaceOrder(r.Context(), session, req.Order)
	if err != nil {
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	s.books.expire(bookPositions, bookOrders, bookLimits)
	s.writeRouteJSON(w, r, http.StatusCreated, raw)
}

// enrichModifyOrder fills in the contract identity that Kotak's modify
// endpoint does not require but the risk checks do.
func (s *Server) enrichModifyOrder(ctx context.Context, session kotakapi.Session, id string, o *kotakapi.Order) error {
	if strings.TrimSpace(o.ExchangeSegment) != "" && strings.TrimSpace(o.TradingSymbol) != "" {
		return nil
	}
	raw, err := s.kotak.Order(ctx, session, id)
	if err != nil {
		return err
	}
	rows, err := decodeBookRows(raw)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errors.New("existing order was not found")
	}
	row := rows[0]
	if strings.TrimSpace(o.ExchangeSegment) == "" {
		o.ExchangeSegment = stringField(row, "exchange_segment", "exchangeSegment", "exSeg")
	}
	if strings.TrimSpace(o.TradingSymbol) == "" {
		o.TradingSymbol = stringField(row, "trading_symbol", "tradingSymbol", "trdSym", "symbol")
	}
	if strings.TrimSpace(o.TransactionType) == "" {
		o.TransactionType = stringField(row, "transaction_type", "transactionType", "trnsTp", "buy_sell")
	}
	if strings.TrimSpace(o.Product) == "" {
		o.Product = stringField(row, "product", "prod")
	}
	if strings.TrimSpace(o.OrderType) == "" {
		o.OrderType = stringField(row, "order_type", "orderType", "ordTp")
	}
	if o.Quantity <= 0 {
		if qty, ok := numberField(row, "quantity", "qty", "order_qty"); ok {
			o.Quantity = int64(qty)
		}
	}
	if o.Price <= 0 {
		if price, ok := numberField(row, "price", "prc"); ok {
			o.Price = price
		}
	}
	return nil
}

func (s *Server) handleBrokerModifyOrder(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeLiveOrder(w, r)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	if err := s.enrichModifyOrder(r.Context(), session, id, &req.Order); err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, "could not load the existing order for risk checks", err)
		return
	}
	if err := s.checkOrderRisk(r.Context(), session, req.Order); err != nil {
		s.writeRiskError(w, r, err)
		return
	}
	raw, err := s.kotak.ModifyOrder(r.Context(), session, id, req.Order)
	if err != nil {
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	s.books.expire(bookOrders)
	s.writeRouteJSON(w, r, http.StatusOK, raw)
}
func (s *Server) handleBrokerCancelOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid cancel request: "+err.Error(), err)
		return
	}
	if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "order id is required", nil)
		return
	}
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	raw, err := s.kotak.CancelOrder(r.Context(), session, id)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	s.books.expire(bookOrders, bookLimits)
	s.writeRouteJSON(w, r, http.StatusOK, raw)
}

type brokerSquareOffAllResponse struct {
	Success         bool     `json:"success"`
	ClosedPositions int      `json:"closed_positions"`
	Errors          []string `json:"errors"`
}

func (s *Server) handleBrokerSquareOffAll(w http.ResponseWriter, r *http.Request) {
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}

	var req struct {
		Confirm bool `json:"confirm"`
	}
	if r.ContentLength > 0 || r.Header.Get("Content-Type") != "" {
		if err := decodeRequest(w, r, &req); err != nil {
			s.writeRouteError(w, r, http.StatusBadRequest, "invalid square-off request: "+err.Error(), err)
			return
		}
	}
	if !req.Confirm {
		s.writeRouteError(w, r, http.StatusBadRequest, "explicit confirmation is required to square off all positions", nil)
		return
	}
	raw, err := s.getBook(r.Context(), bookPositions, session)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
		return
	}
	rows, err := decodeBookRows(raw)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, "failed to decode positions: "+err.Error(), err)
		return
	}

	defaultProduct := s.configSnapshot().Trading.DefaultProduct
	if defaultProduct == "" {
		defaultProduct = "MIS"
	}

	var orders []kotakapi.Order
	for _, row := range rows {
		qty, hasQty := numberField(row, "quantity", "net_qty", "netQty", "qty", "netqty", "net_quantity")
		if !hasQty || qty == 0 {
			bought, _ := numberField(row, "flBuyQty", "fl_buy_qty")
			sold, _ := numberField(row, "flSellQty", "fl_sell_qty")
			carriedBought, _ := numberField(row, "cfBuyQty", "cf_buy_qty")
			carriedSold, _ := numberField(row, "cfSellQty", "cf_sell_qty")
			if net := (bought + carriedBought) - (sold + carriedSold); net != 0 {
				qty = net
			}
		}
		netQty := int64(qty)
		if netQty == 0 {
			continue
		}
		side := "B"
		if netQty > 0 {
			side = "S"
		}
		prod := strings.TrimSpace(stringField(row, "product", "prod", "prd"))
		if prod == "" {
			prod = defaultProduct
		}
		segment := strings.ToLower(strings.TrimSpace(stringField(row, "exchange_segment", "exchangeSegment", "exSeg")))
		symbol := strings.TrimSpace(stringField(row, "trading_symbol", "tradingSymbol", "trdSym", "symbol"))

		orders = append(orders, kotakapi.Order{
			ExchangeSegment: segment,
			TradingSymbol:   symbol,
			TransactionType: side,
			Quantity:        absInt64(netQty),
			Product:         strings.ToUpper(prod),
			OrderType:       "MARKET",
			Price:           0,
			Validity:        "DAY",
		})
	}

	type squareOffResult struct {
		symbol string
		err    error
	}

	results := make(chan squareOffResult, len(orders))
	const maxConcurrent = 5
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, o := range orders {
		wg.Add(1)
		sem <- struct{}{}
		go func(order kotakapi.Order) {
			defer wg.Done()
			defer func() { <-sem }()

			_, orderErr := s.kotak.PlaceOrder(r.Context(), session, order)
			results <- squareOffResult{symbol: order.TradingSymbol, err: orderErr}
		}(o)
	}

	wg.Wait()
	close(results)

	closedCount := 0
	errList := make([]string, 0)
	for res := range results {
		if res.err != nil {
			errMsg := res.err.Error()
			if res.symbol != "" {
				errMsg = fmt.Sprintf("%s: %v", res.symbol, res.err)
			}
			errList = append(errList, errMsg)
			if s.log != nil {
				s.log.Error("square-off order failed", "symbol", res.symbol, "error", res.err)
			}
		} else {
			closedCount++
		}
	}

	if s.bracketEngine != nil {
		if active, err := s.bracketEngine.List(500); err == nil {
			for _, b := range active {
				if b.Status == bracket.StatusInPosition || b.Status == bracket.StatusPendingEntry || b.Status == bracket.StatusExitFailed {
					_ = s.bracketEngine.Disarm(r.Context(), b.ID)
				}
			}
		}
	}

	s.books.expire(bookPositions, bookOrders, bookLimits)

	s.writeRouteJSON(w, r, http.StatusOK, brokerSquareOffAllResponse{
		Success:         len(errList) == 0,
		ClosedPositions: closedCount,
		Errors:          errList,
	})
}

func (s *Server) handleBrokerMarginRequired(w http.ResponseWriter, r *http.Request) {
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	var req kotakapi.MarginRequest
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid margin request: "+err.Error(), err)
		return
	}

	// Verify that InstrumentToken is a numeric token, otherwise look it up from market scrip master
	isNumeric := true
	if req.InstrumentToken == "" {
		isNumeric = false
	} else {
		for _, ch := range req.InstrumentToken {
			if ch < '0' || ch > '9' {
				isNumeric = false
				break
			}
		}
	}
	if !isNumeric && req.TradingSymbol != "" && s.market != nil {
		if opt, found := s.market.FindOption(req.ExchangeSegment, req.TradingSymbol); found && opt.PSymbol != "" {
			req.InstrumentToken = opt.PSymbol
		} else if opt, found := s.market.Option(req.TradingSymbol); found && opt.PSymbol != "" {
			req.InstrumentToken = opt.PSymbol
		}
	}
	if req.InstrumentToken == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "instrument_token or recognizable trading_symbol is required", nil)
		return
	}

	ot := strings.ToUpper(strings.TrimSpace(req.OrderType))
	priceKey := req.Price
	if ot == "MKT" || ot == "MARKET" {
		priceKey = 0.0
	}
	cacheKey := fmt.Sprintf("%s:%s:%s:%s:%s:%s:%d:%.2f", sessionKey(session), req.ExchangeSegment, req.InstrumentToken, req.TransactionType, ot, req.Product, req.Quantity, priceKey)

	s.marginCacheMu.Lock()
	if s.marginCache == nil {
		s.marginCache = make(map[string]marginCacheEntry)
	}
	if entry, ok := s.marginCache[cacheKey]; ok && time.Since(entry.cachedAt) < 10*time.Second {
		payload := entry.payload
		s.marginCacheMu.Unlock()
		s.writeRouteJSON(w, r, http.StatusOK, payload)
		return
	}
	s.marginCacheMu.Unlock()

	raw, err := s.kotak.MarginRequired(r.Context(), session, req)
	if err != nil {
		s.writeRouteError(w, r, http.StatusBadGateway, "margin calculation failed: "+err.Error(), err)
		return
	}

	s.marginCacheMu.Lock()
	if s.marginCache == nil {
		s.marginCache = make(map[string]marginCacheEntry)
	}
	if len(s.marginCache) > 500 {
		s.marginCache = make(map[string]marginCacheEntry)
	}
	s.marginCache[cacheKey] = marginCacheEntry{
		payload:  raw,
		cachedAt: time.Now(),
	}
	s.marginCacheMu.Unlock()

	s.writeRouteJSON(w, r, http.StatusOK, raw)
}

func (s *Server) handleScripSearch(w http.ResponseWriter, r *http.Request) {
	if s.market == nil {
		s.writeRouteJSON(w, r, http.StatusOK, []scrip.SearchResult{})
		return
	}
	q := r.URL.Query()
	limit := 50
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		limit = l
	}
	strike := 0.0
	if st, err := strconv.ParseFloat(q.Get("strike"), 64); err == nil && st > 0 {
		strike = st
	}
	sym := q.Get("symbol")
	if sym == "" {
		sym = q.Get("q")
	}

	results := s.market.SearchScrips(scrip.Query{
		Segment:    q.Get("segment"),
		Symbol:     sym,
		Expiry:     q.Get("expiry"),
		OptionType: q.Get("option_type"),
		Strike:     strike,
		Limit:      limit,
	})
	if results == nil {
		results = []scrip.SearchResult{}
	}
	s.writeRouteJSON(w, r, http.StatusOK, results)
}

func (s *Server) handleDiagnosticsIP(w http.ResponseWriter, r *http.Request) {
	s.diagIPMu.Lock()
	if s.diagIPCache != "" && time.Since(s.diagIPAt) < 60*time.Second {
		ip := s.diagIPCache
		s.diagIPMu.Unlock()
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"ip": ip, "cached": true})
		return
	}
	s.diagIPMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org?format=json", nil)
	if err != nil {
		s.diagIPMu.Lock()
		s.diagIPCache = "unavailable"
		s.diagIPAt = time.Now()
		s.diagIPMu.Unlock()
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"ip": "unavailable", "error": err.Error()})
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.diagIPMu.Lock()
		s.diagIPCache = "unavailable"
		s.diagIPAt = time.Now()
		s.diagIPMu.Unlock()
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"ip": "unavailable", "error": err.Error()})
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	var body struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&body); err != nil || body.IP == "" {
		s.diagIPMu.Lock()
		s.diagIPCache = "unavailable"
		s.diagIPAt = time.Now()
		s.diagIPMu.Unlock()
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"ip": "unavailable"})
		return
	}

	s.diagIPMu.Lock()
	s.diagIPCache = body.IP
	s.diagIPAt = time.Now()
	s.diagIPMu.Unlock()

	s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"ip": body.IP, "cached": false})
}
