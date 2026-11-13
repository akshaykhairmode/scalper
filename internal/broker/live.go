// Package broker provides order-execution adapters for the bracket engine.
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/akshaykhairmode/scalper/internal/bracket"
	"github.com/akshaykhairmode/scalper/internal/kotakapi"
)

// SessionProvider returns the current authenticated broker session, if any.
type SessionProvider func() (kotakapi.Session, bool)

// Live executes orders against the Kotak Neo trading API.
type Live struct {
	Client        *kotakapi.Client
	Session       SessionProvider
	OnOrderChange func()
	// LookupFn resolves an order by broker ID or client tag. It is supplied by
	// the web package because it reuses the account-book parser.
	LookupFn func(ctx context.Context, id, tag string) (string, string, float64, error)
}

// Place submits an order and extracts the broker order ID from the response.
func (l *Live) Place(ctx context.Context, o kotakapi.Order) (string, error) {
	session, ok := l.session()
	if !ok {
		return "", errors.New("kotak session unavailable or not authenticated")
	}
	raw, err := l.Client.PlaceOrder(ctx, session, o)
	if err != nil {
		return "", err
	}
	l.changed()
	var resp struct {
		NOrdNo  string `json:"nOrdNo"`
		OrderID string `json:"order_id"`
		ID      string `json:"id"`
	}
	_ = json.Unmarshal(raw, &resp)
	orderID := resp.NOrdNo
	if orderID == "" {
		orderID = resp.OrderID
	}
	if orderID == "" {
		orderID = resp.ID
	}
	if orderID == "" {
		return "", fmt.Errorf("kotak: order placement succeeded but order ID missing from response: %s", string(raw))
	}
	return orderID, nil
}

// Cancel requests cancellation of one order.
func (l *Live) Cancel(ctx context.Context, id string) error {
	session, ok := l.session()
	if !ok {
		return errors.New("kotak session unavailable or not authenticated")
	}
	if _, err := l.Client.CancelOrder(ctx, session, id); err != nil {
		return err
	}
	l.changed()
	return nil
}

// Modify updates an existing order.
func (l *Live) Modify(ctx context.Context, id string, o kotakapi.Order) error {
	session, ok := l.session()
	if !ok {
		return errors.New("kotak session unavailable or not authenticated")
	}
	if _, err := l.Client.ModifyOrder(ctx, session, id, o); err != nil {
		return err
	}
	l.changed()
	return nil
}

// Lookup resolves an order by broker ID or client tag.
func (l *Live) Lookup(ctx context.Context, id, tag string) (string, string, float64, error) {
	if l.LookupFn == nil {
		return "", "", 0, bracket.ErrExecutorUnavailable
	}
	return l.LookupFn(ctx, id, tag)
}

func (l *Live) session() (kotakapi.Session, bool) {
	if l == nil || l.Client == nil || l.Session == nil {
		return kotakapi.Session{}, false
	}
	return l.Session()
}

func (l *Live) changed() {
	if l.OnOrderChange != nil {
		l.OnOrderChange()
	}
}
