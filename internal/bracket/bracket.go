// Package bracket implements server-side bracket and OCO order lifecycle management.
package bracket

import (
	"context"
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

// Bracket represents a bracket and OCO order configuration and current execution state.
type Bracket struct {
	ID              string    `json:"id"`
	ExchangeSegment string    `json:"exchange_segment"`
	TradingSymbol   string    `json:"trading_symbol"`
	InstrumentKey   string    `json:"instrument_key"`
	TransactionType string    `json:"transaction_type"` // BUY or SELL
	Quantity        int64     `json:"quantity"`
	Product         string    `json:"product"` // MIS or NRML
	EntryPrice      float64   `json:"entry_price"`
	EntryOrderID    string    `json:"entry_order_id"`
	TargetPrice     float64   `json:"target_price"`
	StopLossPrice   float64   `json:"stoploss_price"`
	TrailingStop    float64   `json:"trailing_stop"`
	ExitOrderID     string    `json:"exit_order_id"`
	ExitPrice       float64   `json:"exit_price"`
	Status          string    `json:"status"`
	PnL             float64   `json:"pnl"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
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
