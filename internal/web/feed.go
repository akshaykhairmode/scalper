package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/hsm"
	"github.com/gorilla/websocket"
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
	if s.hsm == nil {
		http.Error(w, "HSM feed is not configured", http.StatusServiceUnavailable)
		return
	}
	conn, err := feedUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "event", "websocket_upgrade_failed", "error", err)
		return
	}
	conn.SetReadLimit(16 << 10)
	if err := conn.WriteJSON(map[string]any{
		"type": "feed_status",
		"data": map[string]string{
			"status": feedStatus(s.hsm.IsConnected()),
		},
	}); err != nil {
		_ = conn.Close()
		return
	}

	s.feedMu.Lock()
	s.feedClients[conn] = struct{}{}
	s.feedMu.Unlock()
	defer func() {
		s.feedMu.Lock()
		delete(s.feedClients, conn)
		s.feedMu.Unlock()
		_ = conn.Close()
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
			if err := s.hsm.Subscribe(command.Keys); err != nil {
				s.log.Warn("HSM subscription failed", "event", "hsm_subscribe_failed", "error", err)
			}
		case "unsubscribe":
			if err := s.hsm.Unsubscribe(command.Keys); err != nil {
				s.log.Warn("HSM unsubscription failed", "event", "hsm_unsubscribe_failed", "error", err)
			}
		default:
			s.log.Warn("unknown browser feed command", "event", "websocket_command_invalid", "type", command.Type)
		}
	}
}

func feedStatus(connected bool) string {
	if connected {
		return "live"
	}
	return "polling"
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
		if !found {
			return "", errors.New("option symbol is not in the Scrip Master")
		}
		return instrument.InstrumentKey, nil
	case "if":
		for _, index := range s.cfg.Market.Indices {
			if index.IndexSegment == parts[1] && index.IndexSymbol == parts[2] {
				return index.IndexSegment + "|" + index.ScripName, nil
			}
		}
		return "", errors.New("index symbol is not configured")
	default:
		return "", errors.New("unsupported HSM topic prefix")
	}
}

func (s *Server) broadcast(value any) {
	s.feedMu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.feedClients))
	for client := range s.feedClients {
		clients = append(clients, client)
	}
	s.feedMu.RUnlock()
	for _, client := range clients {
		_ = client.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := client.WriteJSON(value); err != nil {
			s.feedMu.Lock()
			delete(s.feedClients, client)
			s.feedMu.Unlock()
			_ = client.Close()
		}
	}
}
