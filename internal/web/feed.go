package web

import (
	"encoding/json"
	"errors"
	"net/http"
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
		if strings.EqualFold(origin, "null") {
			return false
		}
		return isAllowedOriginStatic(r, origin)
	},
}

type feedClient struct {
	conn *websocket.Conn
	send chan []byte
}

type feedCommand struct {
	Type string   `json:"type"`
	Keys []string `json:"keys"`
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.configSnapshot().App.ServeMode && s.a != nil && s.a.CurrentSession() == nil {
		http.Error(w, "broker authentication required", http.StatusUnauthorized)
		return
	}
	if s.hsm == nil && s.orderFeed == nil {
		http.Error(w, "market and order feeds are not configured", http.StatusServiceUnavailable)
		return
	}
	upgrader := feedUpgrader
	if s != nil {
		upgrader.CheckOrigin = func(req *http.Request) bool {
			origin := strings.TrimSpace(req.Header.Get("Origin"))
			if origin == "" {
				return true
			}
			if strings.EqualFold(origin, "null") {
				return false
			}
			return s.isAllowedOrigin(req, origin)
		}
	}
	conn, err := upgrader.Upgrade(w, r, nil)
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

	client := &feedClient{
		conn: conn,
		send: make(chan []byte, 256),
	}

	s.feedMu.Lock()
	s.feedClients[client] = struct{}{}
	s.feedMu.Unlock()
	defer func() {
		s.feedMu.Lock()
		delete(s.feedClients, client)
		close(client.send)
		s.feedMu.Unlock()
		if cerr := conn.Close(); cerr != nil && s.log != nil {
			s.log.Debug("failed to close feed client connection", "error", cerr)
		}
	}()

	go func() {
		for {
			select {
			case <-s.feedCtx.Done():
				return
			case msg, ok := <-client.send:
				if !ok {
					return
				}
				if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && s.log != nil {
					s.log.Debug("failed to set write deadline", "error", err)
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			}
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
				s.log.Warn("HSM subscription failed", "event", "hsm_subscribe_failed", "keys", command.Keys, "error", err)
			}
		case "unsubscribe":
			if s.hsm == nil {
				continue
			}
			keys := command.Keys
			// Browser subscriptions are global in the HSM feed, so never let a
			// client unsubscribe a contract that an open AI paper call still
			// needs for live outcome tracking.
			if s.evaluator != nil {
				keys = s.evaluator.FilterUntrackedKeys(keys)
			}
			if len(keys) == 0 {
				continue
			}
			if err := s.hsm.Unsubscribe(keys); err != nil {
				s.log.Warn("HSM unsubscription failed", "event", "hsm_unsubscribe_failed", "keys", keys, "error", err)
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
			// Expire cached orders and positions so the subsequent read fetches
			// fresh status from Kotak, and mark the cache dirty.
			s.books.expire(bookOrders, bookPositions)
			s.books.invalidate()
			if s.bracketEngine != nil {
				s.bracketEngine.OnOrderEvent(s.feedCtx, event)
			}
			s.broadcast(map[string]any{"type": "broker_" + event.Type, "data": event.Data})
		}
	}
}

func (s *Server) forwardHSMEvents() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	pendingTicks := make(map[string]*tickPayload)
	keyCache := make(map[string]string)
	lastDepth := make(map[string]time.Time)

	flush := func() {
		if len(pendingTicks) == 0 {
			return
		}
		if len(pendingTicks) == 1 {
			for _, t := range pendingTicks {
				s.broadcast(tickMessage{Type: "tick", Data: t})
			}
		} else {
			ticks := make([]*tickPayload, 0, len(pendingTicks))
			for _, t := range pendingTicks {
				ticks = append(ticks, t)
			}
			s.broadcast(tickMessage{Type: "ticks", Data: ticks})
		}
		clear(pendingTicks)
	}

	for {
		select {
		case <-s.feedCtx.Done():
			flush()
			return
		case <-ticker.C:
			flush()
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
			cacheKey := event.Quote.InstrumentKey
			if event.Quote.Identifier != "" {
				cacheKey = event.Quote.InstrumentKey + "\x00" + event.Quote.Identifier
			}
			key, ok := keyCache[cacheKey]
			if !ok {
				var err error
				key, err = s.resolveHSMKey(event.Quote)
				if err != nil {
					s.log.Warn("HSM quote could not be mapped", "event", "hsm_quote_unmapped", "error", err,
						"instrument", event.Quote.InstrumentKey)
					continue
				}
				keyCache[cacheKey] = key
			}
			ts := time.Now()
			if event.Quote.Timestamp > 0 {
				if event.Quote.Timestamp > 1e12 {
					ts = time.UnixMilli(event.Quote.Timestamp)
				} else {
					ts = time.Unix(event.Quote.Timestamp, 0)
				}
			}
			s.setLiveQuote(key, event.Quote, ts)
			if s.bracketEngine != nil {
				s.bracketEngine.OnTick(s.feedCtx, key, event.Quote.LTP)
				if event.Quote.InstrumentKey != key {
					s.bracketEngine.OnTick(s.feedCtx, event.Quote.InstrumentKey, event.Quote.LTP)
				}
			}
			if s.evaluator != nil {
				s.evaluator.OnTick(s.feedCtx, key, event.Quote.LTP)
				if event.Quote.InstrumentKey != key {
					s.evaluator.OnTick(s.feedCtx, event.Quote.InstrumentKey, event.Quote.LTP)
				}
			}
			if s.candleAgg != nil {
				s.candleAgg.OnTick(key, event.Quote.LTP, event.Quote.Volume, event.Quote.OI, ts)
			}

			tp := pendingTicks[key]
			if tp == nil {
				tp = &tickPayload{Key: key}
				pendingTicks[key] = tp
			}
			tp.LTP = event.Quote.LTP
			tp.PrevClose = event.Quote.PreviousClose
			tp.Change = event.Quote.Change
			tp.ChangePct = event.Quote.ChangePct
			tp.Volume = event.Quote.Volume
			tp.Bid = event.Quote.Bid
			tp.Ask = event.Quote.Ask
			tp.Timestamp = event.Quote.Timestamp
			if event.Quote.OI >= 0 && event.Quote.OI <= 100_000_000 {
				oi := event.Quote.OI
				tp.OI = &oi
			} else {
				tp.OI = nil
			}
			if len(event.Quote.Bids) > 0 || len(event.Quote.Asks) > 0 {
				// Depth is only needed by the depth tab/chart. Throttle it to
				// 1 Hz instead of shipping 5 levels on every 250 ms tick.
				if now := time.Now(); now.Sub(lastDepth[key]) >= time.Second {
					tp.Bids = event.Quote.Bids
					tp.Asks = event.Quote.Asks
					lastDepth[key] = now
				}
			}
		}
	}
}

type tickMessage struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type tickPayload struct {
	Key       string           `json:"key"`
	LTP       float64          `json:"ltp"`
	PrevClose float64          `json:"prev_close"`
	Change    float64          `json:"change"`
	ChangePct float64          `json:"change_pct"`
	Volume    int64            `json:"volume"`
	Bid       float64          `json:"bid"`
	Ask       float64          `json:"ask"`
	Timestamp int64            `json:"timestamp"`
	OI        *int64           `json:"oi,omitempty"`
	Bids      []hsm.DepthLevel `json:"bids,omitempty"`
	Asks      []hsm.DepthLevel `json:"asks,omitempty"`
}

// liveQuote is a small in-memory quote cache fed by the HSM stream. It lets
// order pricing, depth and AI LTP lookups avoid a REST round trip when a fresh
// tick is already available.
type liveQuote struct {
	LTP     float64
	Bid     float64
	Ask     float64
	Bids    []hsm.DepthLevel
	Asks    []hsm.DepthLevel
	At      time.Time
	DepthAt time.Time
}

func (s *Server) setLiveQuote(key string, q *hsm.Quote, at time.Time) {
	if s == nil || key == "" || q == nil {
		return
	}
	s.liveMu.Lock()
	if s.liveQuotes == nil {
		s.liveQuotes = make(map[string]liveQuote)
	}
	lq, ok := s.liveQuotes[key]
	if !ok && len(s.liveQuotes) >= 2048 {
		for k := range s.liveQuotes {
			delete(s.liveQuotes, k)
			break
		}
	}
	lq.LTP = q.LTP
	lq.Bid = q.Bid
	lq.Ask = q.Ask
	lq.At = at
	// Depth only needs to be fresh enough for order/scan lookups; copying 5
	// levels on every tick is unnecessary.
	if (len(q.Bids) > 0 || len(q.Asks) > 0) && (lq.DepthAt.IsZero() || at.Sub(lq.DepthAt) >= time.Second) {
		lq.Bids = append([]hsm.DepthLevel(nil), q.Bids...)
		lq.Asks = append([]hsm.DepthLevel(nil), q.Asks...)
		lq.DepthAt = at
	}
	s.liveQuotes[key] = lq
	s.liveMu.Unlock()
}

func (s *Server) cachedLTP(key string, maxAge time.Duration) (float64, bool) {
	if s == nil || key == "" {
		return 0, false
	}
	s.liveMu.RLock()
	lq, ok := s.liveQuotes[key]
	s.liveMu.RUnlock()
	if !ok || lq.LTP <= 0 || time.Since(lq.At) > maxAge {
		return 0, false
	}
	return lq.LTP, true
}

func (s *Server) cachedDepth(key string, maxAge time.Duration) ([]hsm.DepthLevel, []hsm.DepthLevel, bool) {
	if s == nil || key == "" {
		return nil, nil, false
	}
	s.liveMu.RLock()
	lq, ok := s.liveQuotes[key]
	s.liveMu.RUnlock()
	if !ok || (len(lq.Bids) == 0 && len(lq.Asks) == 0) || time.Since(lq.DepthAt) > maxAge {
		return nil, nil, false
	}
	return lq.Bids, lq.Asks, true
}

func toMarketDepth(levels []hsm.DepthLevel) []market.DepthLevel {
	if len(levels) == 0 {
		return nil
	}
	out := make([]market.DepthLevel, len(levels))
	for i, level := range levels {
		out[i] = market.DepthLevel{Price: level.Price, Quantity: level.Quantity, Orders: level.Orders}
	}
	return out
}

func (s *Server) resolveHSMKey(quote *hsm.Quote) (string, error) {
	firstPipe := strings.IndexByte(quote.InstrumentKey, '|')
	if firstPipe < 0 {
		return "", errors.New("invalid HSM topic name")
	}
	prefix := quote.InstrumentKey[:firstPipe]
	rest := quote.InstrumentKey[firstPipe+1:]
	secondPipe := strings.IndexByte(rest, '|')
	if secondPipe < 0 {
		return "", errors.New("invalid HSM topic name")
	}
	segment := rest[:secondPipe]
	token := rest[secondPipe+1:]

	switch prefix {
	case "sf":
		symbol := quote.Identifier
		if symbol == "" {
			symbol = token
		}
		instrument, found := s.market.FindOption(segment, symbol)
		if found {
			return instrument.InstrumentKey, nil
		}
		spotResolver, canResolveSpot := s.market.(interface {
			FindSpot(string, string) (scrip.Spot, bool)
		})
		if !canResolveSpot {
			return "", errors.New("market does not support spot-symbol resolution")
		}
		spot, found := spotResolver.FindSpot(segment, symbol)
		if !found {
			return "", errors.New("scrip symbol is not in the Scrip Master")
		}
		return spot.InstrumentKey, nil
	case "if":
		snap := s.configSnapshot()
		for _, index := range snap.Market.Indices {
			if index.IndexSegment == segment && market.IndexQuoteSymbol(index) == token {
				return index.IndexSegment + "|" + index.ScripName, nil
			}
		}
		if snap.Market.IndiaVIX.Enabled {
			vix := snap.Market.IndiaVIX
			vixSymbol := vix.Symbol
			if vixSymbol == "" {
				vixSymbol = "India VIX"
			}
			vixSeg := vix.ExchangeSegment
			if vixSeg == "" {
				vixSeg = "nse_cm"
			}
			if vixSeg == segment && (vixSymbol == token || strings.EqualFold(token, "India VIX") || strings.EqualFold(token, "INDIAVIX") || strings.EqualFold(token, "INDIA VIX")) {
				scripName := vix.ScripName
				if scripName == "" {
					scripName = "INDIAVIX"
				}
				return vixSeg + "|" + scripName, nil
			}
		}
		return "", errors.New("index symbol is not configured")
	default:
		return "", errors.New("unsupported HSM topic prefix")
	}
}

func (s *Server) broadcast(value any) {
	s.feedMu.RLock()
	hasClients := len(s.feedClients) > 0
	s.feedMu.RUnlock()
	if !hasClients {
		return
	}

	data, err := json.Marshal(value)
	if err != nil {
		if s.log != nil {
			s.log.Warn("failed to marshal broadcast message", "error", err)
		}
		return
	}

	s.feedMu.RLock()
	for client := range s.feedClients {
		select {
		case client.send <- data:
		default:
			if s.log != nil {
				s.log.Debug("dropped broadcast message for slow websocket client")
			}
		}
	}
	s.feedMu.RUnlock()
}
