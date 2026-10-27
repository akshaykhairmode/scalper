package broker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"log/slog"
)

// BrokerServer is the HTTP handler for broker operations.
// It provides REST endpoints for the UI to interact with the broker.
type BrokerServer struct {
	log           *slog.Logger
	cfg           *config.Config
	broker        *KotakBroker
	orderStore    *orderStore
	positions     map[string]*Position
	orderChan     chan *Order
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// orderStore manages in-memory order storage.
type orderStore struct {
	mu      sync.RWMutex
	orders  map[string]*Order
	trades  []*Trade
}

// NewBrokerServer creates a new BrokerServer.
func NewBrokerServer(log *slog.Logger, cfg *config.Config, broker *KotakBroker) *BrokerServer {
	bs := &BrokerServer{
		log:       log,
		cfg:       cfg,
		broker:    broker,
		positions: make(map[string]*Position),
		orderChan: make(chan *Order, 100),
		stopCh:    make(chan struct{}),
	}

	// Initialize order store
	bs.orderStore = &orderStore{
		orders: make(map[string]*Order),
	}

	// Start background goroutines
	bs.wg.Add(1)
	go bs.orderProcessor()

	return bs
}

// orderProcessor processes orders from the channel.
func (bs *BrokerServer) orderProcessor() {
	defer bs.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-bs.stopCh:
			return
		case order := <-bs.orderChan:
			// Process the order
			bs.processOrder(order)
		case <-ticker.C:
			// Periodic cleanup or status checks
		}
	}
}

// processOrder processes a single order.
func (bs *BrokerServer) processOrder(order *Order) {
	bs.log.Info("processing order", "event", "broker_order_process",
		"order_id", order.ID,
		"instrument", order.InstrumentKey,
		"quantity", order.Quantity)

	// Check if market hours
	isMarket, _ := bs.broker.IsMarketHours()
	if !isMarket {
		order.Status = "REJECTED"
		order.Reason = "Market is not open"
		bs.log.Warn("order rejected - market closed", "event", "broker_market_closed",
			"order_id", order.ID)
		return
	}

	// Risk check
	if bs.broker.riskMgr != nil {
		if err := bs.broker.riskMgr.CheckOrder(order); err != nil {
			order.Status = "REJECTED"
			order.Reason = err.Error()
			bs.log.Warn("order rejected - risk check failed", "event", "broker_risk_check_failed",
				"order_id", order.ID, "error", err)
			return
		}
	}

	// Place the order via the broker
	if err := bs.broker.PlaceOrder(order); err != nil {
		order.Status = "REJECTED"
		order.Reason = err.Error()
		bs.log.Error("order failed - broker place order error", "event", "broker_place_order_error",
			"order_id", order.ID, "error", err)
		return
	}

	// Order executed successfully
	order.Status = "EXECUTED"

	// Record the trade
	trade := &Trade{
		ID:            order.ID,
		InstrumentKey: order.InstrumentKey,
		Quantity:      order.Quantity,
		Price:         order.Price,
		BuySell:       order.BuySell,
		Timestamp:     time.Now().Unix(),
		Status:        "EXECUTED",
	}

	// Update positions
	bs.updatePositions(order, trade)

	bs.log.Info("order executed successfully", "event", "broker_order_executed",
		"order_id", order.ID,
		"instrument", order.InstrumentKey,
		"quantity", order.Quantity,
		"pnl", trade.PnL)
}

// updatePositions updates the position based on the order and trade.
func (bs *BrokerServer) updatePositions(order *Order, trade *Trade) {
	bs.orderStore.mu.Lock()
	defer bs.orderStore.mu.Unlock()

	instKey := order.InstrumentKey

	// Get or create position
	pos, exists := bs.positions[instKey]
	if !exists {
		pos = &Position{
			InstrumentKey: instKey,
		}
		bs.positions[instKey] = pos
	}

	// Update position based on buy/sell
	if order.BuySell == "B" {
		// Buy order - increase position
		pos.Quantity += order.Quantity
		// Update average price
		if pos.AveragePrice == 0 {
			pos.AveragePrice = order.Price
		} else {
			// Running average
			totalCost := pos.AveragePrice * float64(pos.Quantity - order.Quantity)
			totalCost += order.Price * float64(order.Quantity)
			pos.AveragePrice = totalCost / float64(pos.Quantity)
		}
		pos.MarketValue = pos.AveragePrice * float64(pos.Quantity)
		pos.TodayPL = (order.Price - pos.AveragePrice) * float64(order.Quantity)
	} else if order.BuySell == "S" {
		// Sell order - decrease position (or square off)
		if float64(pos.Quantity) >= float64(order.Quantity) {
			pos.Quantity -= order.Quantity
			pos.MarketValue = pos.AveragePrice * float64(pos.Quantity)
			pos.TodayPL = (pos.AveragePrice - order.Price) * float64(order.Quantity)
		} else {
			// Short sell - negative quantity
			pos.Quantity -= order.Quantity
			pos.MarketValue = pos.AveragePrice * float64(pos.Quantity)
		}
	}

	// Record the trade
	bs.orderStore.trades = append(bs.orderStore.trades, trade)
}

// GetPositions implements broker operations for the server.
func (bs *BrokerServer) GetPositions() ([]Position, error) {
	bs.orderStore.mu.RLock()
	defer bs.orderStore.mu.RUnlock()

	positions := make([]Position, 0, len(bs.positions))
	for _, pos := range bs.positions {
		positions = append(positions, *pos)
	}
	return positions, nil
}

// GetTradeBook implements broker operations for the server.
func (bs *BrokerServer) GetTradeBook() ([]Trade, error) {
	bs.orderStore.mu.RLock()
	defer bs.orderStore.mu.RUnlock()

	trades := make([]Trade, 0, len(bs.orderStore.trades))
	for _, trade := range bs.orderStore.trades {
		trades = append(trades, *trade)
	}
	return trades, nil
}

// GetOrderBook implements broker operations for the server.
func (bs *BrokerServer) GetOrderBook() ([]Order, error) {
	bs.orderStore.mu.RLock()
	defer bs.orderStore.mu.RUnlock()

	orders := make([]Order, 0, len(bs.orderStore.orders))
	for _, order := range bs.orderStore.orders {
		orders = append(orders, *order)
	}
	return orders, nil
}

// SquareOffAll squares off all open positions.
func (bs *BrokerServer) SquareOffAll() error {
	bs.log.Info("square off all triggered", "event", "broker_squareoff_all_request")

	// Get current positions
	positions, err := bs.GetPositions()
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	// Square off each position
	for _, pos := range positions {
		if pos.Quantity != 0 {
			// Determine square-off order direction
			var sqOrder *Order
			if pos.Quantity > 0 {
				// Square off long position - sell
				sqOrder = &Order{
					ID:           fmt.Sprintf("sqo-%d", time.Now().Unix()),
					InstrumentKey: pos.InstrumentKey,
					ExchangeSegment: "nse_fo",
					OrderType:    "MARKET",
					Quantity:     pos.Quantity,
					BuySell:      "S", // Sell to square off long
					Status:       "PENDING",
					Timestamp:    time.Now().Unix(),
				}
			} else {
				// Square off short position - buy
				sqOrder = &Order{
					ID:           fmt.Sprintf("sqo-%d", time.Now().Unix()),
					InstrumentKey: pos.InstrumentKey,
					ExchangeSegment: "nse_fo",
					OrderType:    "MARKET",
					Quantity:     -pos.Quantity, // positive quantity for buy
					BuySell:      "B", // Buy to square off short
					Status:       "PENDING",
					Timestamp:    time.Now().Unix(),
				}
			}

			// Place square-off order
			if err := bs.broker.PlaceOrder(sqOrder); err != nil {
				bs.log.Error("failed to square off position", "event", "broker_squareoff_error",
					"instrument", pos.InstrumentKey, "error", err)
				continue
			}

			bs.log.Info("position squared off", "event", "broker_squareoff_position",
				"instrument", pos.InstrumentKey,
				"quantity", pos.Quantity,
				"direction", sqOrder.BuySell)
		}
	}

	return nil
}

// PlaceOrder places a new order via the UI -> broker -> API flow.
func (bs *BrokerServer) PlaceOrder(order *Order) error {
	bs.log.Info("placing order from UI", "event", "broker_ui_order",
		"order_id", order.ID,
		"instrument", order.InstrumentKey,
		"quantity", order.Quantity,
		"buy_sell", order.BuySell)

	// Send order to broker channel for processing
	select {
	case bs.orderChan <- order:
		// Order queued for processing
		return nil
	default:
		return fmt.Errorf("order channel full, please try again")
	}
}

// GetMargin implements broker operations for the server.
func (bs *BrokerServer) GetMargin() (AvailableMargin, error) {
	return bs.broker.GetMargin()
}

// HealthCheck returns the health status of the broker server.
func (bs *BrokerServer) HealthCheck() map[string]string {
	status := map[string]string{
		"status":     "ok",
		"broker":     "initialized",
		"positions":  fmt.Sprintf("%d", len(bs.positions)),
		"orders":     fmt.Sprintf("%d", len(bs.orderStore.orders)),
		"uptime":     fmt.Sprintf("%v", time.Since(time.Now().Add(-24*time.Hour)).Round(time.Second)),
	}

	// Check if broker is initialized
	if !bs.broker.initialized {
		status["broker"] = "not_initialized"
	}

	return status
}

// Stop stops the broker server and all background goroutines.
func (bs *BrokerServer) Stop() {
	close(bs.stopCh)
	bs.wg.Wait()
	close(bs.orderChan)
}

// RegisterRoutes registers the HTTP routes for the broker server.
func (bs *BrokerServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/broker/positions", func(w http.ResponseWriter, r *http.Request) {
		positions, err := bs.GetPositions()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(positions)
	})

	mux.HandleFunc("GET /api/broker/trade-book", func(w http.ResponseWriter, r *http.Request) {
		trades, err := bs.GetTradeBook()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(trades)
	})

	mux.HandleFunc("GET /api/broker/order-book", func(w http.ResponseWriter, r *http.Request) {
		orders, err := bs.GetOrderBook()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(orders)
	})

	mux.HandleFunc("POST /api/broker/square-off-all", func(w http.ResponseWriter, r *http.Request) {
		if err := bs.SquareOffAll(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	})

	mux.HandleFunc("POST /api/broker/order", func(w http.ResponseWriter, r *http.Request) {
		var order Order
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := bs.PlaceOrder(&order); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	})
}