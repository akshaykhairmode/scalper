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
	"github.com/akshaykhairmode/scalper/internal/paper"
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
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperBook(w, r, bookOrders)
		return
	}
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
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperOrder(w, r, id, false)
		return
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) { return s.kotak.Order(r.Context(), x, id) })
}
func (s *Server) handleBrokerTrades(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperBook(w, r, bookTrades)
		return
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookTrades, x)
	})
}
func (s *Server) handleBrokerPositions(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperBook(w, r, bookPositions)
		return
	}
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
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperBook(w, r, bookHoldings)
		return
	}
	if s.books != nil && (r.URL.Query().Get("fresh") == "true" || r.URL.Query().Get("fresh") == "1") {
		s.books.expire(bookHoldings)
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) {
		return s.getBook(r.Context(), bookHoldings, x)
	})
}
func (s *Server) handleBrokerLimits(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperBook(w, r, bookLimits)
		return
	}
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
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	if mode == modePaper {
		s.writePaperOrder(w, r, id, true)
		return
	}
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) { return s.kotak.OrderHistory(r.Context(), x, id) })
}

type liveOrderRequest struct {
	kotakapi.Order
	InstrumentKey string `json:"instrument_key"`
	Mode          string `json:"mode"`
	Intent        string `json:"intent"`
	Confirm       bool   `json:"confirm"`
}

func (s *Server) decodeLiveOrder(w http.ResponseWriter, r *http.Request) (liveOrderRequest, bool) {
	var req liveOrderRequest
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid broker order request: "+err.Error(), err)
		return req, false
	}
	if strings.TrimSpace(req.InstrumentKey) != "" {
		req.Order.InstrumentKey = strings.TrimSpace(req.InstrumentKey)
	}
	return req, true
}
func (s *Server) handleBrokerPlaceOrder(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeLiveOrder(w, r)
	if !ok {
		return
	}
	mode := s.requestMode(r)
	if strings.TrimSpace(req.Mode) != "" {
		mode = normalizeTradingMode(req.Mode)
	}
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	if mode == modePaper {
		s.placePaperOrder(w, r, req)
		return
	}
	if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
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
	// An explicit exit cancels the exchange backstop first so two sells are
	// never live at the broker. A manual reduce keeps its backstop.
	isExit := strings.EqualFold(strings.TrimSpace(req.Intent), "EXIT")
	if isExit && s.bracketEngine != nil {
		s.bracketEngine.CancelExchangeStopsForInstrument(r.Context(), req.ExchangeSegment, req.TradingSymbol, req.TransactionType)
	}
	raw, err := s.kotak.PlaceOrder(r.Context(), session, req.Order)
	if err != nil {
		if isExit {
			s.rearmLiveBackstopsForContract(r.Context(), req.ExchangeSegment, req.TradingSymbol, req.Product)
		}
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	if isExit {
		if orderID := brokerOrderID(raw); orderID != "" {
			// Disarm only after the exit is confirmed filled, so a rejected
			// exit does not leave the position without its server SL/TP.
			go s.disarmLiveBracketsAfterExit(r.Context(), orderID, req.ExchangeSegment, req.TradingSymbol, req.Product, req.TransactionType, req.Quantity)
		}
	}
	s.books.expire(bookPositions, bookOrders, bookLimits)
	s.writeRouteJSON(w, r, http.StatusCreated, raw)
}

// placePaperOrder simulates an order against the paper account. No broker
// session or Kotak endpoint is used.
func (s *Server) placePaperOrder(w http.ResponseWriter, r *http.Request, req liveOrderRequest) {
	if !s.paperAvailable() {
		s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
		return
	}
	if s.configSnapshot().Trading.Paper.ConfirmOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for paper orders", nil)
		return
	}
	if strings.TrimSpace(req.Order.InstrumentKey) == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "instrument_key is required for paper orders", nil)
		return
	}
	if err := s.checkPaperOrderRisk(r.Context(), req.Order); err != nil {
		s.writeRiskError(w, r, err)
		return
	}
	isExit := strings.EqualFold(strings.TrimSpace(req.Intent), "EXIT")
	if isExit && s.paperEngine != nil {
		s.paperEngine.CancelExchangeStopsForInstrument(r.Context(),
			req.ExchangeSegment, req.TradingSymbol, req.TransactionType)
	}
	product := strings.ToUpper(strings.TrimSpace(req.Product))
	if product == "" {
		product = "MIS"
	}
	req.Product = product
	before := s.paperPositionNet(req.Order.InstrumentKey, product)
	id, err := s.paperExec.Place(r.Context(), req.Order)
	if err != nil {
		if isExit {
			s.rearmPaperBackstopsForContract(r.Context(), req.ExchangeSegment, req.TradingSymbol, product)
		}
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	s.ensurePaperSubscribed(req.Order.InstrumentKey)
	after := s.paperPositionNet(req.Order.InstrumentKey, product)
	if isExit || absInt64(after) < absInt64(before) {
		s.syncPaperBracketsAfterExit(r.Context(), req.Order.InstrumentKey, product, req.ExchangeSegment, req.TradingSymbol)
	}
	status := paper.StatusComplete
	if _, actual, _, lookupErr := s.paperExec.Lookup(r.Context(), id, ""); lookupErr == nil && actual != "" {
		status = actual
	}
	s.writeRouteJSON(w, r, http.StatusCreated, map[string]any{
		"nOrdNo":    id,
		"order_id":  id,
		"id":        id,
		"status":    status,
		"mode":      modePaper,
		"simulated": true,
	})
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
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "order id is required", nil)
		return
	}
	mode := s.requestMode(r)
	if strings.TrimSpace(req.Mode) != "" {
		mode = normalizeTradingMode(req.Mode)
	}
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	if mode == modePaper {
		if !s.paperAvailable() {
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
			return
		}
		if s.configSnapshot().Trading.Paper.ConfirmOrders && !req.Confirm {
			s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for paper orders", nil)
			return
		}
		existing, found := s.paperExec.Order(id)
		if !found {
			s.writeRouteError(w, r, http.StatusNotFound, "paper order not found", nil)
			return
		}
		riskOrder := req.Order
		riskOrder.InstrumentKey = existing.InstrumentKey
		riskOrder.ExchangeSegment = existing.ExchangeSegment
		riskOrder.TradingSymbol = existing.TradingSymbol
		riskOrder.TransactionType = existing.TransactionType
		riskOrder.Product = existing.Product
		if riskOrder.Quantity <= 0 {
			riskOrder.Quantity = existing.Quantity
		}
		if riskOrder.Price <= 0 {
			riskOrder.Price = existing.Price
		}
		if err := s.checkPaperOrderRisk(r.Context(), riskOrder); err != nil {
			s.writeRiskError(w, r, err)
			return
		}
		if err := s.paperExec.Modify(r.Context(), id, req.Order); err != nil {
			s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
			return
		}
		if s.paperEngine != nil {
			s.paperEngine.OnOrderModified(r.Context(), id, req.Price, req.Quantity)
		}
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"order_id": id, "success": true, "mode": modePaper})
		return
	}
	if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
		return
	}
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
	if s.bracketEngine != nil && req.Price > 0 {
		s.bracketEngine.OnOrderModified(r.Context(), id, req.Price, req.Quantity)
	}
	s.books.expire(bookOrders)
	s.writeRouteJSON(w, r, http.StatusOK, raw)
}
func (s *Server) handleBrokerCancelOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool   `json:"confirm"`
		Mode    string `json:"mode"`
	}
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid cancel request: "+err.Error(), err)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "order id is required", nil)
		return
	}
	mode := s.requestMode(r)
	if strings.TrimSpace(req.Mode) != "" {
		mode = normalizeTradingMode(req.Mode)
	}
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	if mode == modePaper {
		if !s.paperAvailable() {
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
			return
		}
		if s.configSnapshot().Trading.Paper.ConfirmOrders && !req.Confirm {
			s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for paper orders", nil)
			return
		}
		if err := s.paperExec.Cancel(r.Context(), id); err != nil {
			s.writeRouteError(w, r, http.StatusBadGateway, err.Error(), err)
			return
		}
		s.writeRouteJSON(w, r, http.StatusOK, map[string]any{"order_id": id, "success": true, "mode": modePaper})
		return
	}
	if s.configSnapshot().Trading.ConfirmLiveOrders && !req.Confirm {
		s.writeRouteError(w, r, http.StatusConflict, "explicit confirmation is required for live orders", nil)
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
	var req struct {
		Confirm bool   `json:"confirm"`
		Mode    string `json:"mode"`
	}
	// Square-off-all is destructive, so require an explicit account instead of
	// falling back to the configured default.
	rawMode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
		if err := decodeRequest(w, r, &req); err != nil {
			s.writeRouteError(w, r, http.StatusBadRequest, "invalid square-off request: "+err.Error(), err)
			return
		}
	}
	if strings.TrimSpace(req.Mode) != "" {
		rawMode = strings.TrimSpace(req.Mode)
	}
	if rawMode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode is required for square-off-all (live or paper)", nil)
		return
	}
	mode := normalizeTradingMode(rawMode)
	if mode == "" {
		s.writeRouteError(w, r, http.StatusBadRequest, "mode must be live or paper", nil)
		return
	}
	if mode == modePaper {
		if !s.paperAvailable() {
			s.writeRouteError(w, r, http.StatusServiceUnavailable, "paper account is not enabled", nil)
			return
		}
		if !req.Confirm {
			s.writeRouteError(w, r, http.StatusBadRequest, "explicit confirmation is required to square off all positions", nil)
			return
		}
		if s.paperEngine != nil {
			s.paperEngine.CancelAllExchangeStops(r.Context())
		}
		closed, errs := s.squareOffPaper(r.Context())
		s.writeRouteJSON(w, r, http.StatusOK, brokerSquareOffAllResponse{
			Success:         len(errs) == 0,
			ClosedPositions: closed,
			Errors:          errs,
		})
		return
	}
	session, ok := s.brokerSession(w, r)
	if !ok {
		return
	}
	if !req.Confirm {
		s.writeRouteError(w, r, http.StatusBadRequest, "explicit confirmation is required to square off all positions", nil)
		return
	}
	// Cancel exchange backstops first: otherwise square-off sells would race
	// live SL-M orders and could create unintended shorts.
	if s.bracketEngine != nil {
		s.bracketEngine.CancelAllExchangeStops(r.Context())
	}

	// Disarm brackets before closing so a tick cannot trigger a second exit
	// while the square-off orders are in flight. In-position brackets are
	// re-armed below if their close order fails.
	errList := make([]string, 0)
	var disarmed []bracket.Bracket
	if s.bracketEngine != nil {
		active, err := s.bracketEngine.List(500)
		if err != nil {
			errList = append(errList, "list brackets: "+err.Error())
		} else {
			for _, b := range active {
				switch b.Status {
				case bracket.StatusPendingEntry:
					if err := s.bracketEngine.Disarm(r.Context(), b.ID); err != nil {
						errList = append(errList, b.TradingSymbol+": "+err.Error())
					}
				case bracket.StatusInPosition, bracket.StatusExitFailed:
					if err := s.bracketEngine.Disarm(r.Context(), b.ID); err != nil {
						errList = append(errList, b.TradingSymbol+": "+err.Error())
						continue
					}
					disarmed = append(disarmed, b)
				}
			}
		}
	}

	raw, err := s.getBook(r.Context(), bookPositions, session)
	if err != nil {
		errList = append(errList, "fetch positions: "+err.Error())
		errList = append(errList, s.rearmAllLiveBrackets(r.Context(), disarmed)...)
		s.writeRouteJSON(w, r, http.StatusBadGateway, brokerSquareOffAllResponse{
			Success: false,
			Errors:  errList,
		})
		return
	}
	rows, err := decodeBookRows(raw)
	if err != nil {
		errList = append(errList, "decode positions: "+err.Error())
		errList = append(errList, s.rearmAllLiveBrackets(r.Context(), disarmed)...)
		s.writeRouteJSON(w, r, http.StatusBadGateway, brokerSquareOffAllResponse{
			Success: false,
			Errors:  errList,
		})
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
		segment string
		symbol  string
		product string
		err     error
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
			results <- squareOffResult{segment: order.ExchangeSegment, symbol: order.TradingSymbol, product: order.Product, err: orderErr}
		}(o)
	}

	wg.Wait()
	close(results)

	closedCount := 0
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
			// The position is still open: restore its bracket and backstop.
			errList = append(errList, s.rearmLiveBrackets(r.Context(), disarmed, res.segment, res.symbol, res.product)...)
			continue
		}
		closedCount++
		// Catch brackets that became IN_POSITION while the close was in flight.
		errList = append(errList, s.disarmLiveBracketsForContract(r.Context(), res.segment, res.symbol, res.product)...)
	}

	s.books.expire(bookPositions, bookOrders, bookLimits)

	s.writeRouteJSON(w, r, http.StatusOK, brokerSquareOffAllResponse{
		Success:         len(errList) == 0,
		ClosedPositions: closedCount,
		Errors:          errList,
	})
}

func (s *Server) handleBrokerMarginRequired(w http.ResponseWriter, r *http.Request) {
	mode, ok := s.requireRequestMode(w, r)
	if !ok {
		return
	}
	paperMode := mode == modePaper
	var session kotakapi.Session
	if !paperMode {
		var ok bool
		session, ok = s.brokerSession(w, r)
		if !ok {
			return
		}
	}
	var req kotakapi.MarginRequest
	if err := decodeRequest(w, r, &req); err != nil {
		s.writeRouteError(w, r, http.StatusBadRequest, "invalid margin request: "+err.Error(), err)
		return
	}
	if paperMode {
		price := req.Price
		instrumentKey := ""
		if option, ok := s.findOrderOption(kotakapi.Order{
			ExchangeSegment: req.ExchangeSegment,
			TradingSymbol:   req.TradingSymbol,
		}); ok {
			instrumentKey = option.InstrumentKey
			if price <= 0 {
				if ltp, ok := s.cachedLTP(option.InstrumentKey, 10*time.Second); ok {
					price = ltp
				}
			}
		}
		s.writeRouteJSON(w, r, http.StatusOK, s.paperMarginPreview(price, req.Quantity, req.TransactionType, instrumentKey))
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
