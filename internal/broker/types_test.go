package broker

import (
	"strings"
	"testing"
)

func TestUnimplementedBrokerAPIsDoNotReturnFakeData(t *testing.T) {
	broker := &KotakBroker{initialized: true}

	if _, err := broker.GetPositions(); err == nil {
		t.Fatal("GetPositions returned a success-shaped empty result")
	}
	if _, err := broker.GetTradeBook(); err == nil {
		t.Fatal("GetTradeBook returned a success-shaped empty result")
	}
	if _, err := broker.GetOrderBook(); err == nil {
		t.Fatal("GetOrderBook returned a success-shaped empty result")
	}
	if _, err := broker.GetMargin(); err == nil {
		t.Fatal("GetMargin returned fake margin values")
	}
	if _, err := broker.GetQuote("NIFTY"); err == nil {
		t.Fatal("GetQuote returned a fake quote")
	}
	if _, err := broker.GetLTP("NIFTY"); err == nil {
		t.Fatal("GetLTP returned a fake quote")
	}

	order := &Order{InstrumentKey: "NIFTY", Quantity: 75, BuySell: "B", OrderType: "MARKET"}
	if err := broker.PlaceOrder(order); err == nil {
		t.Fatal("PlaceOrder reported an unimplemented order as successful")
	}
	if order.Status != "" {
		t.Fatalf("PlaceOrder changed status to %q despite returning no real result", order.Status)
	}
	if err := broker.ModifyOrder(order); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("ModifyOrder error = %v, want explicit not-implemented error", err)
	}
	if err := broker.CancelOrder("test-order"); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("CancelOrder error = %v, want explicit not-implemented error", err)
	}
	if err := broker.SquareOffAll(); err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("SquareOffAll error = %v, want explicit not-implemented error", err)
	}
}
