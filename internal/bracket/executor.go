package bracket

import (
	"context"
	"errors"

	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

// Executor submits, cancels and inspects orders for one trading account.
// The live broker and the paper simulator each provide an implementation.
type Executor interface {
	Place(ctx context.Context, o kotakapi.Order) (id string, err error)
	Cancel(ctx context.Context, id string) error
	Modify(ctx context.Context, id string, o kotakapi.Order) error
	Lookup(ctx context.Context, id, tag string) (foundID, status string, price float64, err error)
}

// Fill is a normalized order update delivered to the engine by any executor.
// Status uses the broker's canonical status strings (COMPLETE, CANCELLED, ...).
type Fill struct {
	OrderID string
	Tag     string
	Status  string
	Price   float64
	Qty     int64
}

// ErrExecutorUnavailable reports that an executor does not implement an
// optional operation. Executors that support every operation never return it.
var ErrExecutorUnavailable = errors.New("bracket: executor operation is not configured")
