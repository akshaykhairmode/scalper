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
