package broker

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"log/slog"
)

//nolint:revive // Preserve the established exported interface name.
type BrokerInterface interface {
	// GetPositions returns the current positions held.
	GetPositions() ([]Position, error)
	// GetTradeBook returns the trade book (history of executed trades).
	GetTradeBook() ([]Trade, error)
	// GetOrderBook returns the current open orders.
	GetOrderBook() ([]Order, error)
	// GetMargin returns the available and used margin.
	GetMargin() (AvailableMargin, error)
	// SquareOffAll squares off all open positions.
	SquareOffAll() error
	// PlaceOrder places a new order.
	PlaceOrder(order *Order) error
	// ModifyOrder modifies an existing order.
	ModifyOrder(order *Order) error
	// CancelOrder cancels an existing order.
	CancelOrder(orderID string) error
	// GetQuote gets the latest quote for an instrument.
	GetQuote(instrumentKey string) (float64, error)
	// IsMarketHours checks if the market is currently open.
	IsMarketHours() (bool, error)
	// GetLTP gets the last traded price for an instrument.
	GetLTP(instrumentKey string) (float64, error)
}

// Position represents a holdings position.
type Position struct {
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Symbol          string  `json:"symbol"`
	Quantity        int64   `json:"quantity"`
	AveragePrice    float64 `json:"average_price"`
	MarketValue     float64 `json:"market_value"`
	PnL             float64 `json:"pnl"`
	TodayPL         float64 `json:"today_pnl"`
}

// Trade represents a executed trade.
type Trade struct {
	ID              string  `json:"id"`
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Quantity        int64   `json:"quantity"`
	Price           float64 `json:"price"`
	BuySell         string  `json:"buy_sell"` // "B" for buy, "S" for sell
	Timestamp       int64   `json:"timestamp"`
	Status          string
	PnL             float64 `json:"status"`
}

// Order represents a trading order.
type Order struct {
	ID              string  `json:"id"`
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	OrderType       string  `json:"order_type"` // "MARKET", "LIMIT", etc.
	Quantity        int64   `json:"quantity"`
	Price           float64 `json:"price"`    // for LIMIT orders
	BuySell         string  `json:"buy_sell"` // "B" for buy, "S" for sell
	Status          string  `json:"status"`   // "PENDING", "EXECUTED", "CANCELLED", "REJECTED"
	Timestamp       int64   `json:"timestamp"`
	TriggerPrice    float64 `json:"trigger_price"`    // for SL orders
	Reason          string  `json:"reason,omitempty"` // rejection reason
}

// AvailableMargin represents the available and required margin.
type AvailableMargin struct {
	TotalMargin     float64 `json:"total_margin"`     // total margin available
	UsedMargin      float64 `json:"used_margin"`      // margin currently used
	AvailableMargin float64 `json:"available_margin"` // margin available for new orders
	FundsAvailable  float64 `json:"funds_available"`  // funds available for trading
}

// RiskManager defines the risk management rules.
type RiskManager interface {
	// CheckOrder checks if an order passes risk checks.
	CheckOrder(order *Order) error
	// CheckSquareOff checks if square-off is allowed.
	CheckSquareOff() error
	// CheckMargin checks if sufficient margin is available.
	CheckMargin(requiredMargin float64) error
}

// KotakBroker implements BrokerInterface for Kotak Neo.
type KotakBroker struct {
	mu          sync.Mutex
	api         *APIClient
	riskMgr     RiskManager
	logger      *slog.Logger
	initialized bool
}

// APIClient represents the Kotak Neo API client.
type APIClient struct {
	baseURL     string
	accessToken string
	client      *http.Client
}

// Session represents the user session.
type Session struct {
	Username     string
	AccessToken  string
	TokenExpiry  time.Time
	LoginMethod  string // "totp" or "mpin"
	PaperMode    bool
	Connected    bool
	LastActivity time.Time
}

// NewKotakBroker creates a new KotakBroker instance.
func NewKotakBroker(cfg *Config, riskMgr RiskManager) *KotakBroker {
	return &KotakBroker{
		api:         NewAPIClient(cfg),
		riskMgr:     riskMgr,
		logger:      slog.Default(),
		initialized: false,
	}
}

// Config holds the broker configuration.
type Config struct {
	AccessToken            string
	BaseURL                string
	PaperMode              bool
	MaxOrdersPerSecond     int
	MaxExitOrdersPerSecond int
	SquareOffTime          string // e.g., "15:30:00"
}

// NewAPIClient creates a new API client.
func NewAPIClient(cfg *Config) *APIClient {
	return &APIClient{
		baseURL:     cfg.BaseURL,
		accessToken: cfg.AccessToken,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

// GetPositions implements BrokerInterface.
func (kb *KotakBroker) GetPositions() ([]Position, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return nil, fmt.Errorf("broker not initialized")
	}

	return nil, fmt.Errorf("positions API is not implemented")
}

// GetTradeBook implements BrokerInterface.
func (kb *KotakBroker) GetTradeBook() ([]Trade, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return nil, fmt.Errorf("broker not initialized")
	}

	return nil, fmt.Errorf("trade book API is not implemented")
}

// GetOrderBook implements BrokerInterface.
func (kb *KotakBroker) GetOrderBook() ([]Order, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return nil, fmt.Errorf("broker not initialized")
	}

	return nil, fmt.Errorf("order book API is not implemented")
}

// GetMargin implements BrokerInterface.
func (kb *KotakBroker) GetMargin() (AvailableMargin, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return AvailableMargin{}, fmt.Errorf("broker not initialized")
	}

	return AvailableMargin{}, fmt.Errorf("margin API is not implemented")
}

// SquareOffAll implements BrokerInterface.
func (kb *KotakBroker) SquareOffAll() error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return fmt.Errorf("broker not initialized")
	}

	return fmt.Errorf("square-off API is not implemented")
}

// PlaceOrder implements BrokerInterface.
func (kb *KotakBroker) PlaceOrder(order *Order) error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return fmt.Errorf("broker not initialized")
	}

	// Risk check
	if kb.riskMgr != nil {
		if err := kb.riskMgr.CheckOrder(order); err != nil {
			return fmt.Errorf("risk check failed: %w", err)
		}
	}

	return fmt.Errorf("order API is not implemented")
}

// ModifyOrder implements BrokerInterface.
func (kb *KotakBroker) ModifyOrder(_ *Order) error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return fmt.Errorf("broker not initialized")
	}

	return fmt.Errorf("modify order API is not implemented")
}

// CancelOrder implements BrokerInterface.
func (kb *KotakBroker) CancelOrder(_ string) error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return fmt.Errorf("broker not initialized")
	}

	return fmt.Errorf("cancel order API is not implemented")
}

// GetQuote implements BrokerInterface.
func (kb *KotakBroker) GetQuote(instrumentKey string) (float64, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return 0, fmt.Errorf("broker not initialized")
	}

	return 0, fmt.Errorf("quote API is not implemented for %s", instrumentKey)
}

// IsMarketHours implements BrokerInterface.
func (kb *KotakBroker) IsMarketHours() (bool, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return false, fmt.Errorf("broker not initialized")
	}

	// In a real implementation, this would check market hours
	// Kotak Neo market hours: 9:15 AM to 3:30 PM IST (NSE)
	// For now, return true during day hours
	now := time.Now()
	hour := now.Hour()
	// Market is open from 9:15 to 15:30 IST
	isOpen := hour >= 9 && hour < 15
	// Also check if it's exactly 15:00-15:30 (closing window)
	if hour == 15 {
		isOpen = now.Minute() < 30
	}

	return isOpen, nil
}

// GetLTP implements BrokerInterface.
func (kb *KotakBroker) GetLTP(instrumentKey string) (float64, error) {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	if !kb.initialized {
		return 0, fmt.Errorf("broker not initialized")
	}

	return 0, fmt.Errorf("quote API is not implemented for %s", instrumentKey)
}

// CheckOrder implements RiskManager.
func (kb *KotakBroker) CheckOrder(order *Order) error {
	// Validate order parameters
	if order.Quantity <= 0 {
		return fmt.Errorf("invalid quantity: %d", order.Quantity)
	}

	if order.BuySell != "B" && order.BuySell != "S" {
		return fmt.Errorf("invalid buy_sell: %s", order.BuySell)
	}

	if order.OrderType == "MARKET" && order.Price > 0 {
		// Market orders should not have a price set
		return fmt.Errorf("market order should not have price set")
	}

	// Check if market hours
	isMarket, err := kb.IsMarketHours()
	if err != nil {
		return err
	}
	if !isMarket {
		return fmt.Errorf("market is not open")
	}

	// Check square-off timing
	// Square off happens at market close, so intraday square-off is allowed
	// but overnight positions might be restricted

	return nil
}

// CheckSquareOff implements RiskManager.
func (kb *KotakBroker) CheckSquareOff() error {
	// Square off is allowed during market hours
	// Overnight square off might require special permissions
	return nil
}

// CheckMargin implements RiskManager.
func (kb *KotakBroker) CheckMargin(requiredMargin float64) error {
	kb.mu.Lock()
	defer kb.mu.Unlock()

	// Get current margin status
	margin, err := kb.GetMargin()
	if err != nil {
		return err
	}

	if requiredMargin > margin.AvailableMargin {
		return fmt.Errorf("insufficient margin: required %.2f, available %.2f",
			requiredMargin, margin.AvailableMargin)
	}

	return nil
}
