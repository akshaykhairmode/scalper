// Package bracket implements server-side bracket and OCO order lifecycle management.
package bracket

import (
	"context"
	"strings"
	"time"
)

// Bracket lifecycle statuses.
const (
	StatusPendingEntry = "PENDING_ENTRY"
	StatusInPosition   = "IN_POSITION"
	StatusTargetHit    = "TARGET_HIT"
	StatusSLHit        = "SL_HIT"
	StatusCancelled    = "CANCELLED"
	StatusExitedManual = "EXITED_MANUAL"
	StatusExitFailed   = "EXIT_FAILED"
)

// Exchange backstop stop-loss modes.
const (
	ExchangeStopOff = "OFF"
	ExchangeStopSLM = "SL-M"
	ExchangeStopSLL = "SL"
)

// Exchange backstop order statuses.
const (
	ExchangeStopPlacing    = "PLACING"
	ExchangeStopWorking    = "WORKING"
	ExchangeStopCancelling = "CANCELLING"
	ExchangeStopFilled     = "FILLED"
	ExchangeStopCancelled  = "CANCELLED"
	ExchangeStopRejected   = "REJECTED"
	ExchangeStopFailed     = "FAILED"
)

// Bracket represents a bracket and OCO order configuration and current execution state.
type Bracket struct {
	ID              string  `json:"id"`
	Mode            string  `json:"mode,omitempty"` // live or paper
	ExchangeSegment string  `json:"exchange_segment"`
	TradingSymbol   string  `json:"trading_symbol"`
	InstrumentKey   string  `json:"instrument_key"`
	TransactionType string  `json:"transaction_type"` // BUY or SELL
	Quantity        int64   `json:"quantity"`
	Product         string  `json:"product"` // MIS or NRML
	EntryPrice      float64 `json:"entry_price"`
	EntryOrderID    string  `json:"entry_order_id"`
	TargetPrice     float64 `json:"target_price"`
	StopLossPrice   float64 `json:"stoploss_price"`
	TrailingStop    float64 `json:"trailing_stop"`
	ExitOrderID     string  `json:"exit_order_id"`
	ExitPrice       float64 `json:"exit_price"`
	Status          string  `json:"status"`
	PnL             float64 `json:"pnl"`
	// ExchangeStopMode is the per-order choice: "" inherits the global default,
	// "OFF" disables it explicitly, "SL-M"/"SL" request an exchange backstop.
	ExchangeStopMode    string    `json:"exchange_stop_mode,omitempty"`
	ExchangeStopOrderID string    `json:"exchange_stop_order_id,omitempty"`
	ExchangeStopPrice   float64   `json:"exchange_stop_price"`
	ExchangeStopStatus  string    `json:"exchange_stop_status,omitempty"`
	Underlying          string    `json:"underlying,omitempty"`
	Strike              float64   `json:"strike,omitempty"`
	OptionType          string    `json:"option_type,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// HasExchangeStop reports whether an exchange backstop order was recorded.
func (b *Bracket) HasExchangeStop() bool {
	return b != nil && strings.TrimSpace(b.ExchangeStopOrderID) != ""
}

// ExchangeStopIsWorking reports whether the exchange backstop may still be live.
func (b *Bracket) ExchangeStopIsWorking() bool {
	if b == nil {
		return false
	}
	switch b.ExchangeStopStatus {
	case ExchangeStopPlacing, ExchangeStopWorking, ExchangeStopCancelling:
		return true
	default:
		return false
	}
}

// Store defines persistence operations for brackets.
type Store interface {
	CreateBracket(ctx context.Context, b Bracket) error
	UpdateBracket(ctx context.Context, b Bracket) error
	GetBracket(ctx context.Context, id string) (*Bracket, error)
	ListBrackets(ctx context.Context, limit int) ([]Bracket, error)
}

// ActiveStore is an optional interface extension for stores that support retrieving active brackets.
type ActiveStore interface {
	Store
	GetActiveBrackets(ctx context.Context) ([]Bracket, error)
}
