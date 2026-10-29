package kotakapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestValidation(t *testing.T) {
	valid := Order{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26OCT25000CE",
		TransactionType: "B",
		OrderType:       "LIMIT",
		Product:         "MIS",
		Quantity:        25,
		Price:           100.5,
		TriggerPrice:    0,
		Validity:        "DAY",
	}

	if err := validateOrder(valid); err != nil {
		t.Fatalf("expected valid order, got %v", err)
	}

	badSeg := valid
	badSeg.ExchangeSegment = ""
	if err := validateOrder(badSeg); err == nil {
		t.Fatal("expected error on empty exchange segment")
	}

	badQty := valid
	badQty.Quantity = 0
	if err := validateOrder(badQty); err == nil {
		t.Fatal("expected error on zero quantity")
	}

	badSide := valid
	badSide.TransactionType = "INVALID"
	if err := validateOrder(badSide); err == nil {
		t.Fatal("expected error on invalid transaction type")
	}

	badType := valid
	badType.OrderType = "FOOBAR"
	if err := validateOrder(badType); err == nil {
		t.Fatal("expected error on invalid order type")
	}

	badProduct := valid
	badProduct.Product = "FOOBAR"
	if err := validateOrder(badProduct); err == nil {
		t.Fatal("expected error on invalid product")
	}

	badPrice := valid
	badPrice.Price = -10
	if err := validateOrder(badPrice); err == nil {
		t.Fatal("expected error on negative price")
	}

	if err := validateModification(valid); err != nil {
		t.Fatalf("expected valid modification, got %v", err)
	}

	badModQty := valid
	badModQty.Quantity = -1
	if err := validateModification(badModQty); err == nil {
		t.Fatal("expected error on negative quantity modification")
	}

	badModType := valid
	badModType.OrderType = "UNKNOWN"
	if err := validateModification(badModType); err == nil {
		t.Fatal("expected error on invalid order type modification")
	}

	badModPrice := valid
	badModPrice.Price = -1
	if err := validateModification(badModPrice); err == nil {
		t.Fatal("expected error on negative price modification")
	}

	if orderType("MARKET") != "MKT" || orderType("MKT") != "MKT" {
		t.Errorf("orderType MARKET = %s", orderType("MARKET"))
	}
	if orderType("LIMIT") != "L" || orderType("L") != "L" {
		t.Errorf("orderType LIMIT = %s", orderType("LIMIT"))
	}
	if orderType("SL") != "SL" {
		t.Errorf("orderType SL = %s", orderType("SL"))
	}
	if orderType("SL-M") != "SL-M" || orderType("SLM") != "SL-M" {
		t.Errorf("orderType SL-M = %s", orderType("SL-M"))
	}
	if orderType("OTHER") != "OTHER" {
		t.Errorf("orderType OTHER = %s", orderType("OTHER"))
	}

	if validity("") != "DAY" || validity("ioc") != "IOC" {
		t.Errorf("validity = %s", validity(""))
	}
}

func TestClientEndpoints(t *testing.T) {
	client := &Client{
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				recorder := httptest.NewRecorder()
				switch {
				case strings.Contains(req.URL.Path, "quick/user/orders/ord123"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","orderId":"ord123"}`))
				case strings.Contains(req.URL.Path, "quick/user/orders"):
					if req.URL.Query().Get("empty") == "1" {
						recorder.WriteHeader(http.StatusOK)
						_, _ = recorder.Write([]byte(`{"stat":"Not_Ok","stCode":5203,"emsg":"No data"}`))
					} else {
						recorder.WriteHeader(http.StatusOK)
						_, _ = recorder.Write([]byte(`{"stat":"Ok","data":[]}`))
					}
				case strings.Contains(req.URL.Path, "quick/user/trades"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","data":[]}`))
				case strings.Contains(req.URL.Path, "portfolio/v2/positions"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","data":[]}`))
				case strings.Contains(req.URL.Path, "portfolio/v1/holdings"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","data":[]}`))
				case strings.Contains(req.URL.Path, "quick/user/limits"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","limits":{}}`))
				case strings.Contains(req.URL.Path, "quick/order/history"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","history":[]}`))
				case strings.Contains(req.URL.Path, "quick/order/cancel"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","result":"cancelled"}`))
				case strings.Contains(req.URL.Path, "quick/order/rule/ms/place"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","nOrdNo":"999"}`))
				case strings.Contains(req.URL.Path, "quick/order/vr/modify"):
					recorder.WriteHeader(http.StatusOK)
					_, _ = recorder.Write([]byte(`{"stat":"Ok","result":"modified"}`))
				default:
					recorder.WriteHeader(http.StatusNotFound)
					_, _ = recorder.Write([]byte(`{"emsg":"not found"}`))
				}
				return recorder.Result(), nil
			}),
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	session := Session{
		BaseURL:     "https://api.kotaksecurities.com",
		SID:         "sid-123",
		Token:       "tok-123",
		ConsumerKey: "ck-123",
	}
	ctx := context.Background()

	// Orders
	orders, err := client.Orders(ctx, session)
	if err != nil || len(orders) == 0 {
		t.Fatalf("Orders failed: %v", err)
	}

	// Order
	order, err := client.Order(ctx, session, "ord123")
	if err != nil || len(order) == 0 {
		t.Fatalf("Order failed: %v", err)
	}

	// Trades
	trades, err := client.Trades(ctx, session)
	if err != nil || len(trades) == 0 {
		t.Fatalf("Trades failed: %v", err)
	}

	// Positions
	positions, err := client.Positions(ctx, session)
	if err != nil || len(positions) == 0 {
		t.Fatalf("Positions failed: %v", err)
	}

	// Holdings
	holdings, err := client.Holdings(ctx, session)
	if err != nil || len(holdings) == 0 {
		t.Fatalf("Holdings failed: %v", err)
	}

	// Limits
	limits, err := client.Limits(ctx, session)
	if err != nil || len(limits) == 0 {
		t.Fatalf("Limits failed: %v", err)
	}

	// OrderHistory
	hist, err := client.OrderHistory(ctx, session, "ord123")
	if err != nil || len(hist) == 0 {
		t.Fatalf("OrderHistory failed: %v", err)
	}

	// CancelOrder
	cancel, err := client.CancelOrder(ctx, session, "ord123")
	if err != nil || len(cancel) == 0 {
		t.Fatalf("CancelOrder failed: %v", err)
	}

	// PlaceOrder
	place, err := client.PlaceOrder(ctx, session, Order{
		ExchangeSegment: "nse_fo",
		TradingSymbol:   "NIFTY26OCT25000CE",
		TransactionType: "B",
		OrderType:       "MKT",
		Product:         "MIS",
		Quantity:        25,
		Tag:             "test-tag",
	})
	if err != nil || len(place) == 0 {
		t.Fatalf("PlaceOrder failed: %v", err)
	}

	// ModifyOrder
	mod, err := client.ModifyOrder(ctx, session, "ord123", Order{
		OrderType: "LIMIT",
		Quantity:  50,
		Price:     110.0,
	})
	if err != nil || len(mod) == 0 {
		t.Fatalf("ModifyOrder failed: %v", err)
	}

	// Untrusted host check
	untrustedSession := session
	untrustedSession.BaseURL = "https://example.com"
	if _, err := client.Orders(ctx, untrustedSession); err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("expected untrusted error, got %v", err)
	}

	// Nil / missing session checks
	if _, err := client.Orders(ctx, Session{}); err == nil {
		t.Fatal("expected error on empty session")
	}

	var nilClient *Client
	if _, err := nilClient.Orders(ctx, session); err == nil {
		t.Fatal("expected error on nil client")
	}

	if _, err := client.Order(ctx, session, ""); err == nil {
		t.Fatal("expected error on empty order ID")
	}
	if _, err := client.Order(ctx, session, "bad/id"); err == nil {
		t.Fatal("expected error on order ID with slash")
	}

	if _, err := client.PlaceOrder(ctx, session, Order{Quantity: 0}); err == nil {
		t.Fatal("expected validation error for PlaceOrder")
	}

	if _, err := client.ModifyOrder(ctx, session, "", Order{Quantity: 10, OrderType: "MKT"}); err == nil {
		t.Fatal("expected error for empty order ID in ModifyOrder")
	}
	if _, err := client.ModifyOrder(ctx, session, "123", Order{Quantity: 0}); err == nil {
		t.Fatal("expected validation error for ModifyOrder")
	}

	// Helper tests
	if !isEmptyReport("quick/user/orders", responseEnvelope{StCode: []byte("5203")}) {
		t.Error("expected isEmptyReport true for code 5203")
	}
	if !isEmptyReport("quick/user/trades", responseEnvelope{ErrMsg: "data not found"}) {
		t.Error("expected isEmptyReport true for data not found")
	}
	if isEmptyReport("portfolio/v2/positions", responseEnvelope{StCode: []byte("5203")}) {
		t.Error("expected isEmptyReport false for other paths")
	}

	errKotak := kotakAPIError(responseEnvelope{Emsg: "limit reached", StCode: []byte("4001"), Desc: "description"})
	if errKotak == nil || !strings.Contains(errKotak.Error(), "limit reached") {
		t.Errorf("unexpected kotakAPIError: %v", errKotak)
	}

	errResp := kotakResponseError(http.StatusInternalServerError, []byte(`{"emsg":"internal error"}`))
	if errResp == nil || !strings.Contains(errResp.Error(), "internal error") {
		t.Errorf("unexpected kotakResponseError: %v", errResp)
	}

	if d := retryAfter(nil, 0); d <= 0 {
		t.Errorf("retryAfter nil: %v", d)
	}
	headerResp := &http.Response{Header: http.Header{"Retry-After": []string{"2"}}}
	if d := retryAfter(headerResp, 0); d != 2*time.Second {
		t.Errorf("retryAfter header: %v", d)
	}
}
