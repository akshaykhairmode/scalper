package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
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
		row["instrument_key"], _ = json.Marshal(option.InstrumentKey)
		row["exchange_segment"], _ = json.Marshal(option.ExchangeSegment)
		row["symbol"], _ = json.Marshal(option.PTrdSymbol)
		row["underlying"], _ = json.Marshal(option.Underlying)
		row["expiry"], _ = json.Marshal(option.Expiry)
		row["strike"], _ = json.Marshal(float64(option.StrikePaise) / 100)
		row["option_type"], _ = json.Marshal(string(option.OptionType))
		row["lot_size"], _ = json.Marshal(option.LotSize)
		if quote, ok := quotes[option.InstrumentKey]; ok && quote.LTP > 0 {
			row["ltp"], _ = json.Marshal(quote.LTP)
		}
		rows[i], _ = json.Marshal(row)
	}
	envelope["data"], _ = json.Marshal(rows)
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
	s.brokerRead(w, r, func(x kotakapi.Session) (json.RawMessage, error) { return s.kotak.Holdings(r.Context(), x) })
}
func (s *Server) handleBrokerLimits(w http.ResponseWriter, r *http.Request) {
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
	if s.cfg.Trading.ConfirmLiveOrders && !req.Confirm {
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
	raw, err := s.kotak.PlaceOrder(r.Context(), session, req.Order)
	if err != nil {
		s.writeRouteError(w, r, http.StatusUnprocessableEntity, err.Error(), err)
		return
	}
	s.books.expire(bookPositions, bookOrders, bookLimits)
	s.writeRouteJSON(w, r, http.StatusCreated, raw)
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
	if s.cfg.Trading.ConfirmLiveOrders && !req.Confirm {
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
