package market

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

type Service struct {
	cfg    config.Config
	scrips *scrip.Manager
	client *QuotesClient
}

func NewService(cfg config.Config, scripts *scrip.Manager, client *QuotesClient) (*Service, error) {
	if scripts == nil {
		return nil, errors.New("market: scrip master is required")
	}
	if client == nil {
		return nil, errors.New("market: Kotak quotes client is required")
	}
	return &Service{cfg: cfg, scrips: scripts, client: client}, nil
}

type IndexQuote struct {
	Key             string  `json:"key"`
	DisplayName     string  `json:"display_name"`
	ExchangeSegment string  `json:"exchange_segment"`
	IndexSegment    string  `json:"index_segment"`
	IndexSymbol     string  `json:"index_symbol"`
	InstrumentKey   string  `json:"instrument_key"`
	QuoteAvailable  bool    `json:"quote_available"`
	LTP             float64 `json:"ltp"`
	PreviousClose   float64 `json:"prev_close"`
	Change          float64 `json:"change"`
	ChangePct       float64 `json:"change_pct"`
	Volume          int64   `json:"volume"`
	UpdatedAt       int64   `json:"updated_at"`
}

type OptionQuote struct {
	InstrumentKey   string  `json:"instrument_key"`
	ExchangeSegment string  `json:"exchange_segment"`
	Underlying      string  `json:"underlying"`
	Expiry          string  `json:"expiry"`
	Strike          float64 `json:"strike"`
	OptionType      string  `json:"option_type"`
	LotSize         int64   `json:"lot_size"`
	Symbol          string  `json:"symbol"`
	PSymbol         string  `json:"p_symbol"`
	PTrdSymbol      string  `json:"p_trd_symbol"`
	LTP             float64 `json:"ltp"`
	PreviousClose   float64 `json:"prev_close"`
	Change          float64 `json:"change"`
	ChangePct       float64 `json:"change_pct"`
	Volume          int64   `json:"volume"`
	OI              int64   `json:"oi"`
	Bid             float64 `json:"bid"`
	Ask             float64 `json:"ask"`
	QuoteAvailable  bool    `json:"quote_available"`
}

type ChainRow struct {
	Strike float64      `json:"strike"`
	Call   *OptionQuote `json:"call,omitempty"`
	Put    *OptionQuote `json:"put,omitempty"`
}

type keyedInstrument struct {
	key        string
	instrument QuoteInstrument
}

func (s *Service) Indices(ctx context.Context, baseURL string) ([]IndexQuote, error) {
	instruments := make([]QuoteInstrument, 0, len(s.cfg.Market.Indices))
	for _, index := range s.cfg.Market.Indices {
		instruments = append(instruments, QuoteInstrument{
			ExchangeSegment: index.IndexSegment,
			Symbol:          index.IndexSymbol,
		})
	}
	quotes, err := s.fetch(ctx, baseURL, instruments)
	if err != nil {
		return nil, err
	}
	result := make([]IndexQuote, 0, len(s.cfg.Market.Indices))
	available := 0
	for _, index := range s.cfg.Market.Indices {
		quote, ok := quotes[quoteKey(index.IndexSegment, index.IndexSymbol)]
		item := IndexQuote{
			Key:             index.Key,
			DisplayName:     index.DisplayName,
			ExchangeSegment: index.ExchangeSegment,
			IndexSegment:    index.IndexSegment,
			IndexSymbol:     index.IndexSymbol,
			InstrumentKey:   index.IndexSegment + "|" + index.ScripName,
			QuoteAvailable:  ok,
		}
		if ok {
			available++
			item.LTP = quote.LTP
			item.PreviousClose = quote.PreviousClose
			item.Change = quote.Change
			item.ChangePct = quote.ChangePct
			item.Volume = quote.Volume
			item.UpdatedAt = quote.UpdatedAt
		}
		result = append(result, item)
	}
	if available == 0 {
		return nil, errors.New("Kotak returned no live quotes for configured indices")
	}
	return result, nil
}

func (s *Service) Expiries(indexKey string) ([]scrip.ExpiryInfo, error) {
	if _, ok := s.configuredIndex(indexKey); !ok {
		return nil, fmt.Errorf("unknown index %q", indexKey)
	}
	expiries := s.scrips.Expiries(indexKey)
	if len(expiries) == 0 {
		return nil, fmt.Errorf("no Scrip Master expiries available for %s", indexKey)
	}
	return expiries, nil
}

func (s *Service) Chain(ctx context.Context, baseURL, indexKey, expiry string) ([]ChainRow, error) {
	if _, ok := s.configuredIndex(indexKey); !ok {
		return nil, fmt.Errorf("unknown index %q", indexKey)
	}
	if !s.validExpiry(indexKey, expiry) {
		return nil, fmt.Errorf("expiry %q is unavailable for %s", expiry, indexKey)
	}
	options := s.scrips.Options(indexKey, expiry)
	if len(options) == 0 {
		return nil, fmt.Errorf("no option contracts in the Scrip Master for %s %s", indexKey, expiry)
	}

	keyed := make([]keyedInstrument, 0, len(options))
	for _, option := range options {
		keyed = append(keyed, keyedInstrument{
			key: option.InstrumentKey,
			instrument: QuoteInstrument{
				ExchangeSegment: option.ExchangeSegment,
				Symbol:          option.PSymbol,
			},
		})
	}
	quotes, err := s.fetchKeyed(ctx, baseURL, keyed)
	if err != nil {
		return nil, err
	}
	byStrike := make(map[int64]*ChainRow, len(options)/2)
	strikes := make([]int64, 0, len(options)/2)
	for _, option := range options {
		row := byStrike[option.StrikePaise]
		if row == nil {
			row = &ChainRow{Strike: float64(option.StrikePaise) / 100}
			byStrike[option.StrikePaise] = row
			strikes = append(strikes, option.StrikePaise)
		}
		quote, found := quotes[option.InstrumentKey]
		converted := &OptionQuote{
			InstrumentKey:   option.InstrumentKey,
			ExchangeSegment: option.ExchangeSegment,
			Underlying:      option.Underlying,
			Expiry:          option.Expiry,
			Strike:          float64(option.StrikePaise) / 100,
			OptionType:      string(option.OptionType),
			LotSize:         option.LotSize,
			Symbol:          option.PTrdSymbol,
			PSymbol:         option.PSymbol,
			PTrdSymbol:      option.PTrdSymbol,
			QuoteAvailable:  found,
		}
		if found {
			converted.LTP = quote.LTP
			converted.PreviousClose = quote.PreviousClose
			converted.Change = quote.Change
			converted.ChangePct = quote.ChangePct
			converted.Volume = quote.Volume
			converted.OI = quote.OI
			converted.Bid = quote.Bid
			converted.Ask = quote.Ask
		}
		switch option.OptionType {
		case scrip.Call:
			row.Call = converted
		case scrip.Put:
			row.Put = converted
		}
	}
	sortStrikeRows(strikes, byStrike)
	result := make([]ChainRow, 0, len(strikes))
	for _, strike := range strikes {
		result = append(result, *byStrike[strike])
	}
	return result, nil
}

func (s *Service) Quote(ctx context.Context, baseURL, instrumentKey string) (Quote, error) {
	option, ok := s.scrips.Option(instrumentKey)
	if !ok {
		return Quote{}, fmt.Errorf("unknown option instrument %q", instrumentKey)
	}
	quotes, err := s.fetch(ctx, baseURL, []QuoteInstrument{{
		ExchangeSegment: option.ExchangeSegment,
		Symbol:          option.PSymbol,
	}})
	if err != nil {
		return Quote{}, err
	}
	quote, ok := quotes[quoteKey(option.ExchangeSegment, option.PSymbol)]
	if !ok {
		return Quote{}, fmt.Errorf("Kotak returned no live quote for %s", option.PTrdSymbol)
	}
	if quote.LTP <= 0 {
		return Quote{}, fmt.Errorf("Kotak live quote for %s has no positive LTP", option.PTrdSymbol)
	}
	return quote, nil
}

func (s *Service) Option(instrumentKey string) (scrip.Option, bool) {
	return s.scrips.Option(instrumentKey)
}

func (s *Service) FindOption(segment, symbol string) (scrip.Option, bool) {
	return s.scrips.FindOption(segment, symbol)
}

func (s *Service) fetch(ctx context.Context, baseURL string, instruments []QuoteInstrument) (map[string]Quote, error) {
	keyed := make([]keyedInstrument, 0, len(instruments))
	for _, instrument := range instruments {
		keyed = append(keyed, keyedInstrument{
			key:        quoteKey(instrument.ExchangeSegment, instrument.Symbol),
			instrument: instrument,
		})
	}
	return s.fetchKeyed(ctx, baseURL, keyed)
}

func (s *Service) fetchKeyed(ctx context.Context, baseURL string, instruments []keyedInstrument) (map[string]Quote, error) {
	batchSize := s.cfg.Market.Chain.BatchSize
	if batchSize < 1 {
		return nil, errors.New("market chain batch_size must be positive")
	}
	result := make(map[string]Quote, len(instruments))
	for start := 0; start < len(instruments); start += batchSize {
		end := start + batchSize
		if end > len(instruments) {
			end = len(instruments)
		}
		batch := instruments[start:end]
		requests := make([]QuoteInstrument, 0, len(batch))
		for _, item := range batch {
			requests = append(requests, item.instrument)
		}
		quotes, err := s.client.Fetch(ctx, baseURL, requests)
		if err != nil {
			return nil, err
		}
		for _, item := range batch {
			if quote, ok := findQuote(quotes, item.instrument); ok {
				result[item.key] = quote
			}
		}
	}
	return result, nil
}

func findQuote(quotes map[string]Quote, instrument QuoteInstrument) (Quote, bool) {
	if quote, ok := quotes[quoteKey(instrument.ExchangeSegment, instrument.Symbol)]; ok {
		return quote, true
	}
	if quote, ok := quotes[normalizeSymbol(instrument.Symbol)]; ok {
		return quote, true
	}
	return Quote{}, false
}

func quoteKey(segment, symbol string) string {
	return strings.ToUpper(strings.TrimSpace(segment)) + "|" + normalizeSymbol(symbol)
}

func (s *Service) configuredIndex(key string) (config.Index, bool) {
	for _, index := range s.cfg.Market.Indices {
		if index.Key == key {
			return index, true
		}
	}
	return config.Index{}, false
}

func (s *Service) validExpiry(indexKey, expiry string) bool {
	for _, item := range s.scrips.Expiries(indexKey) {
		if item.Date == expiry && !item.Expired {
			return true
		}
	}
	return false
}

func sortStrikeRows(strikes []int64, rows map[int64]*ChainRow) {
	for i := 1; i < len(strikes); i++ {
		for j := i; j > 0 && strikes[j] < strikes[j-1]; j-- {
			strikes[j], strikes[j-1] = strikes[j-1], strikes[j]
		}
	}
}
