package paper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

type Account struct {
	StartingCash float64 `json:"starting_cash"`
	Cash         float64 `json:"cash"`
	RealisedPnL  float64 `json:"realised_pnl"`
	Available    float64 `json:"available_margin"`
	Used         float64 `json:"used_margin"`
}

type Position struct {
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Symbol          string  `json:"symbol"`
	Underlying      string  `json:"underlying"`
	Expiry          string  `json:"expiry"`
	Strike          float64 `json:"strike"`
	OptionType      string  `json:"option_type"`
	LotSize         int64   `json:"lot_size"`
	Quantity        int64   `json:"quantity"`
	AveragePrice    float64 `json:"average_price"`
	LTP             float64 `json:"ltp"`
	MarketValue     float64 `json:"market_value"`
	PnL             float64 `json:"pnl"`
	Product         string  `json:"product"`
}

type Order struct {
	ID              string  `json:"id"`
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Underlying      string  `json:"underlying"`
	Expiry          string  `json:"expiry"`
	Strike          float64 `json:"strike"`
	OptionType      string  `json:"option_type"`
	Symbol          string  `json:"symbol"`
	Side            string  `json:"side"`
	Quantity        int64   `json:"quantity"`
	Price           float64 `json:"price"`
	Status          string  `json:"status"`
	Reason          string  `json:"reason,omitempty"`
	Timestamp       int64   `json:"timestamp"`
	Product         string  `json:"product"`
	Mode            string  `json:"mode"`
}

type Trade struct {
	ID            int64   `json:"id"`
	InstrumentKey string  `json:"instrument_key"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Quantity      int64   `json:"quantity"`
	Price         float64 `json:"price"`
	ExecutedAt    int64   `json:"executed_at"`
}

type Snapshot struct {
	Account   Account    `json:"account"`
	Positions []Position `json:"positions"`
	Orders    []Order    `json:"orders"`
	Trades    []Trade    `json:"trades"`
}

type OrderRequest struct {
	ClientOrderID string
	Instrument    scrip.Option
	Timestamp     int64
	Side          string
	Quantity      int64
	Product       string
	Intent        string
	FillPrice     float64
}

type Repository interface {
	EnsurePaperAccount(context.Context, float64) error
	PaperSnapshot(context.Context, float64) (Snapshot, error)
	ApplyPaperTrade(context.Context, OrderRequest, float64) (Order, error)
}

type Service struct {
	repository Repository
	cfg        config.Trading
	now        func() time.Time
}

func NewService(ctx context.Context, repository Repository, cfg config.Trading) (*Service, error) {
	if repository == nil {
		return nil, errors.New("paper: repository is required")
	}
	if cfg.Paper.StartingCash <= 0 || math.IsNaN(cfg.Paper.StartingCash) || math.IsInf(cfg.Paper.StartingCash, 0) {
		return nil, errors.New("paper: starting cash must be positive and finite")
	}
	if cfg.Paper.ShortMarginPct < 0 || cfg.Paper.ShortMarginPct > 100 {
		return nil, errors.New("paper: short margin percentage must be between 0 and 100")
	}
	if err := repository.EnsurePaperAccount(ctx, cfg.Paper.StartingCash); err != nil {
		return nil, fmt.Errorf("paper: initialize account: %w", err)
	}
	return &Service{repository: repository, cfg: cfg, now: time.Now}, nil
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.repository.PaperSnapshot(ctx, s.cfg.Paper.ShortMarginPct)
	if err != nil {
		return Snapshot{}, fmt.Errorf("paper: load account: %w", err)
	}
	return snapshot, nil
}

func (s *Service) PlaceOrder(ctx context.Context, request OrderRequest) (Order, error) {
	if request.Instrument.InstrumentKey == "" || request.Instrument.PSymbol == "" {
		return Order{}, errors.New("paper: instrument is not in the Scrip Master")
	}
	if request.Side != "B" && request.Side != "S" {
		return Order{}, errors.New("paper: side must be B or S")
	}
	if request.Quantity <= 0 || request.Instrument.LotSize <= 0 ||
		request.Quantity%request.Instrument.LotSize != 0 {
		return Order{}, fmt.Errorf("paper: quantity must be a positive multiple of lot size %d", request.Instrument.LotSize)
	}
	if request.Product != "MIS" && request.Product != "NRML" {
		return Order{}, errors.New("paper: product must be MIS or NRML")
	}
	request.Intent = strings.ToUpper(strings.TrimSpace(request.Intent))
	if request.Intent == "" {
		request.Intent = "ENTRY"
	}
	if request.Intent != "ENTRY" && request.Intent != "EXIT" {
		return Order{}, errors.New("paper: intent must be ENTRY or EXIT")
	}
	if request.FillPrice <= 0 || math.IsNaN(request.FillPrice) || math.IsInf(request.FillPrice, 0) {
		return Order{}, errors.New("paper: a positive live quote is required to fill the order")
	}
	id, err := newOrderID()
	if err != nil {
		return Order{}, fmt.Errorf("paper: create order ID: %w", err)
	}
	request.ClientOrderID = id
	request.Timestamp = s.now().Unix()
	request.Instrument.PTrdSymbol = strings.TrimSpace(request.Instrument.PTrdSymbol)
	order, err := s.repository.ApplyPaperTrade(ctx, request, s.cfg.Paper.ShortMarginPct)
	if err != nil {
		return Order{}, fmt.Errorf("paper: execute order: %w", err)
	}
	return order, nil
}

func newOrderID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "paper-" + hex.EncodeToString(raw[:]), nil
}
