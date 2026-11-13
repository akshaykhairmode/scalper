// Package paper implements the server-side paper trading account. Orders are
// filled locally from live feed ticks; no broker order API is called.
package paper

import (
	"context"
	"time"
)

// Order statuses used by the paper book.
const (
	StatusOpen           = "OPEN"
	StatusTriggerPending = "TRIGGER_PENDING"
	StatusComplete       = "COMPLETE"
	StatusCancelled      = "CANCELLED"
	StatusRejected       = "REJECTED"
)

// Order is one simulated order.
type Order struct {
	ID              string
	BracketID       string
	ExchangeSegment string
	TradingSymbol   string
	InstrumentKey   string
	TransactionType string // BUY or SELL
	Product         string
	OrderType       string // MKT, L, SL, SL-M
	Quantity        int64
	FilledQuantity  int64
	Price           float64
	TriggerPrice    float64
	FilledPrice     float64
	Status          string
	Validity        string // DAY or IOC
	Tag             string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Open reports whether the order can still fill.
func (o Order) Open() bool {
	return o.Status == StatusOpen || o.Status == StatusTriggerPending
}

// Trade is one simulated execution.
type Trade struct {
	ID              int64
	OrderID         string
	InstrumentKey   string
	TradingSymbol   string
	TransactionType string
	Quantity        int64
	Price           float64
	ExecutedAt      time.Time
}

// Position is the net paper position for one instrument.
type Position struct {
	InstrumentKey   string
	ExchangeSegment string
	TradingSymbol   string
	Product         string
	NetQuantity     int64
	AveragePrice    float64
	RealizedPnL     float64
	UpdatedAt       time.Time
}

// Account is the paper cash/PnL ledger.
type Account struct {
	StartingBalance float64
	RealizedPnL     float64
	UpdatedAt       time.Time
}

// Quote is the latest observed price for one instrument.
type Quote struct {
	LTP float64
	Bid float64
	Ask float64
	At  time.Time
}

// Store persists the paper account and books.
type Store interface {
	EnsurePaperAccount(ctx context.Context, startingBalance float64) (Account, error)
	CreatePaperOrder(ctx context.Context, o Order) error
	UpdatePaperOrder(ctx context.Context, o Order) error
	GetPaperOrder(ctx context.Context, id string) (*Order, error)
	ListPaperOrders(ctx context.Context, limit int) ([]Order, error)
	ListOpenPaperOrders(ctx context.Context) ([]Order, error)
	// RecordPaperFill persists one fill atomically: order state, position,
	// account ledger and trade row.
	RecordPaperFill(ctx context.Context, o Order, p Position, a Account, t Trade) error
	// CreatePaperFill inserts an already-complete order and its fill atomically.
	// It is used for market orders so a failed fill never leaves an OPEN row.
	CreatePaperFill(ctx context.Context, o Order, p Position, a Account, t Trade) error
	ListPaperTrades(ctx context.Context, limit int) ([]Trade, error)
	ListPaperPositions(ctx context.Context) ([]Position, error)
}
