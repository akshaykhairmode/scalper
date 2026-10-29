package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/akshaykhairmode/scalper/internal/hsm"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

var feedUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		return err == nil && parsed.Host == r.Host &&
			(parsed.Scheme == "http" || parsed.Scheme == "https")
	},
}

type feedCommand struct {
	Type string   `json:"type"`
	Keys []string `json:"keys"`
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.hsm == nil && s.orderFeed == nil {
		http.Error(w, "market and order feeds are not configured", http.StatusServiceUnavailable)
		return
	}
	conn, err := feedUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "event", "websocket_upgrade_failed", "error", err)
		return
	}
	conn.SetReadLimit(16 << 10)
	status := "polling"
	if s.hsm != nil && s.hsm.IsConnected() {
		status = "live"
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "feed_status",
		"data": map[string]string{
			"status": status,
		},
	}); err != nil {
		if cerr := conn.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close client connection on feed status error", "error", cerr)
		}
		return
	}
	orderStatus := "disconnected"
	if s.orderFeed != nil {
		orderStatus = "connecting"
		if s.orderFeed.Connected() {
			orderStatus = "live"
		}
	}
	if err := conn.WriteJSON(map[string]any{"type": "order_feed_status", "data": map[string]string{"status": orderStatus}}); err != nil {
		if cerr := conn.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close client connection on order feed status error", "error", cerr)
		}
		return
	}

	s.feedMu.Lock()
	s.feedClients[conn] = struct{}{}
	s.feedMu.Unlock()
	defer func() {
		s.feedMu.Lock()
		delete(s.feedClients, conn)
		s.feedMu.Unlock()
		if cerr := conn.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close feed client connection", "error", cerr)
		}
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("browser websocket closed", "event", "websocket_closed", "error", err)
			}
			return
		}
		var command feedCommand
		if err := json.Unmarshal(raw, &command); err != nil {
			s.log.Warn("invalid browser feed command", "event", "websocket_command_invalid", "error", err)
			continue
		}
		switch command.Type {
		case "subscribe":
			if s.hsm == nil {
				continue
			}
			if err := s.hsm.Subscribe(command.Keys); err != nil {
				s.log.Warn("HSM subscription failed", "event", "hsm_subscribe_failed", "error", err)
			}
		case "unsubscribe":
			if s.hsm == nil {
				continue
			}
			if err := s.hsm.Unsubscribe(command.Keys); err != nil {
				s.log.Warn("HSM unsubscription failed", "event", "hsm_unsubscribe_failed", "error", err)
			}
		default:
			s.log.Warn("unknown browser feed command", "event", "websocket_command_invalid", "type", command.Type)
		}
	}
}

func (s *Server) forwardOrderFeedEvents() {
	for {
		select {
		case <-s.feedCtx.Done():
			return
		case event := <-s.orderFeed.Events():
			if event.Status != "" {
				s.broadcast(map[string]any{"type": "order_feed_status", "data": map[string]string{"status": event.Status}})
				continue
			}
			if len(event.Data) == 0 {
				continue
			}
			// Let the shared book cache decide when to re-read Kotak; the
			// browser only receives the event and reads the cache.
			s.books.invalidate()
			s.broadcast(map[string]any{"type": "broker_" + event.Type, "data": event.Data})
		}
	}
}

func (s *Server) forwardHSMEvents() {
	for {
		select {
		case <-s.feedCtx.Done():
			return
		case event := <-s.hsm.Events():
			if event.Status != "" {
				s.broadcast(map[string]any{
					"type": "feed_status",
					"data": map[string]string{"status": event.Status},
				})
				continue
			}
			if event.Quote == nil {
				continue
			}
			key, err := s.resolveHSMKey(event.Quote)
			if err != nil {
				s.log.Warn("HSM quote could not be mapped", "event", "hsm_quote_unmapped", "error", err,
					"instrument", event.Quote.InstrumentKey)
				continue
			}
			s.broadcast(map[string]any{
				"type": "tick",
				"data": map[string]any{
					"key": key, "ltp": event.Quote.LTP,
					"prev_close": event.Quote.PreviousClose,
					"change":     event.Quote.Change,
					"change_pct": event.Quote.ChangePct,
					"volume":     event.Quote.Volume, "oi": event.Quote.OI,
					"bid": event.Quote.Bid, "ask": event.Quote.Ask,
					"timestamp": event.Quote.Timestamp,
				},
			})
		}
	}
}

func (s *Server) resolveHSMKey(quote *hsm.Quote) (string, error) {
	parts := strings.SplitN(quote.InstrumentKey, "|", 3)
	if len(parts) != 3 {
		return "", errors.New("invalid HSM topic name")
	}
	switch parts[0] {
	case "sf":
		symbol := quote.Identifier
		if symbol == "" {
			symbol = parts[2]
		}
		instrument, found := s.market.FindOption(parts[1], symbol)
		if found {
			return instrument.InstrumentKey, nil
		}
		spotResolver, canResolveSpot := s.market.(interface {
			FindSpot(string, string) (scrip.Spot, bool)
		})
		if !canResolveSpot {
			return "", errors.New("market does not support spot-symbol resolution")
		}
		spot, found := spotResolver.FindSpot(parts[1], symbol)
		if !found {
			return "", errors.New("scrip symbol is not in the Scrip Master")
		}
		return spot.InstrumentKey, nil
	case "if":
		for _, index := range s.cfg.Market.Indices {
			if index.IndexSegment == parts[1] && market.IndexQuoteSymbol(index) == parts[2] {
				return index.IndexSegment + "|" + index.ScripName, nil
			}
		}
		return "", errors.New("index symbol is not configured")
	default:
		return "", errors.New("unsupported HSM topic prefix")
	}
}

func (s *Server) broadcast(value any) {
	s.feedWriteMu.Lock()
	defer s.feedWriteMu.Unlock()
	s.feedMu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.feedClients))
	for client := range s.feedClients {
		clients = append(clients, client)
	}
	s.feedMu.RUnlock()
	for _, client := range clients {
		if err := client.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && s.log != nil {
			s.log.Debug("failed to set write deadline for broadcast client", "error", err)
		}
		if err := client.WriteJSON(value); err != nil {
			s.feedMu.Lock()
			delete(s.feedClients, client)
			s.feedMu.Unlock()
			if cerr := client.Close(); cerr != nil && s.log != nil {
				s.log.Debug("failed to close client after write error", "error", cerr)
			}
		}
	}
}
