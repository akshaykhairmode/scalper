package market

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/scrip"
)

// Service combines Scrip Master data with Kotak market quotes.
type Service struct {
	cfg    config.Config
	scrips *scrip.Manager
	client *QuotesClient
}

// NewService creates a market data service.
func NewService(cfg config.Config, scripts *scrip.Manager, client *QuotesClient) (*Service, error) {
	if scripts == nil {
		return nil, errors.New("market: scrip master is required")
	}
	if client == nil {
		return nil, errors.New("market: Kotak quotes client is required")
	}
	return &Service{cfg: cfg, scrips: scripts, client: client}, nil
}

// IndexQuote is a normalized quote for a configured market index.
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

// OptionQuote is a normalized quote for an option contract.
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

// ChainRow holds the call and put quotes for one strike.
type ChainRow struct {
	Strike float64      `json:"strike"`
	Call   *OptionQuote `json:"call,omitempty"`
	Put    *OptionQuote `json:"put,omitempty"`
}

// ChainPage is one selectively loaded strike window.
type ChainPage struct {
	Rows         []ChainRow `json:"rows"`
	HasMoreAbove bool       `json:"has_more_above"`
	HasMoreBelow bool       `json:"has_more_below"`
}

// Candle is one normalized historical OHLCV bar.
type Candle struct {
	Time         int64   `json:"time"`
	Open         float64 `json:"open"`
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Close        float64 `json:"close"`
	Volume       int64   `json:"volume"`
	OpenInterest int64   `json:"oi,omitempty"`
}

type keyedInstrument struct {
	key        string
	instrument QuoteInstrument
}

// IndexQuoteSymbol returns Kotak's exact quote/feed name for a configured index.
func IndexQuoteSymbol(index config.Index) string {
	if strings.EqualFold(strings.TrimSpace(index.IndexSymbol), "Nifty Midcap Select") {
		return "NIFTY MID SELECT"
	}
	return index.IndexSymbol
}

// Indices fetches configured index quotes and the nearest MCX futures quote for
// each commodity. Commodity futures share the header ticker shape with index
// spots, but their pSymbol comes from the Scrip Master.
func (s *Service) Indices(ctx context.Context, baseURL string) ([]IndexQuote, error) {
	instruments := make([]QuoteInstrument, 0, len(s.cfg.Market.Indices)+len(s.cfg.Market.Commodities))
	for _, index := range s.cfg.Market.Indices {
		instruments = append(instruments, QuoteInstrument{
			ExchangeSegment: index.IndexSegment,
			Symbol:          IndexQuoteSymbol(index),
		})
	}
	commoditySpots := make(map[string]scrip.Spot, len(s.cfg.Market.Commodities))
	for _, commodity := range s.cfg.Market.Commodities {
		name := strings.TrimSpace(commodity.ScripName)
		if name == "" {
			name = commodity.Key
		}
		spot, ok := s.scrips.Spot(scrip.SpotKey(commodity.ExchangeSegment, name))
		if !ok {
			continue
		}
		commoditySpots[commodity.Key] = spot
		instruments = append(instruments, QuoteInstrument{
			ExchangeSegment: spot.ExchangeSegment,
			Symbol:          spot.PSymbol,
		})
	}
	quotes, err := s.fetch(ctx, baseURL, instruments)
	if err != nil {
		return nil, err
	}
	result := make([]IndexQuote, 0, len(s.cfg.Market.Indices)+len(s.cfg.Market.Commodities))
	available := 0
	for _, index := range s.cfg.Market.Indices {
		indexSymbol := IndexQuoteSymbol(index)
		quote, ok := quotes[quoteKey(index.IndexSegment, indexSymbol)]
		spot, hasSpot := s.scrips.Spot(scrip.SpotKey(index.IndexSegment, index.ScripName))
		item := IndexQuote{
			Key:             index.Key,
			DisplayName:     index.DisplayName,
			ExchangeSegment: index.ExchangeSegment,
			IndexSegment:    index.IndexSegment,
			IndexSymbol:     indexSymbol,
			InstrumentKey:   index.IndexSegment + "|" + index.ScripName,
			QuoteAvailable:  ok,
		}
		if hasSpot {
			item.InstrumentKey = spot.InstrumentKey
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
	for _, commodity := range s.cfg.Market.Commodities {
		spot, hasSpot := commoditySpots[commodity.Key]
		item := IndexQuote{
			Key:             commodity.Key,
			DisplayName:     commodity.DisplayName,
			ExchangeSegment: commodity.ExchangeSegment,
			IndexSegment:    commodity.ExchangeSegment,
			IndexSymbol:     spot.Symbol,
			QuoteAvailable:  false,
		}
		if hasSpot {
			item.InstrumentKey = spot.InstrumentKey
			quote, ok := quotes[quoteKey(spot.ExchangeSegment, spot.PSymbol)]
			item.QuoteAvailable = ok
			if ok {
				available++
				item.LTP = quote.LTP
				item.PreviousClose = quote.PreviousClose
				item.Change = quote.Change
				item.ChangePct = quote.ChangePct
				item.Volume = quote.Volume
				item.UpdatedAt = quote.UpdatedAt
			}
		}
		result = append(result, item)
	}
	if available == 0 {
		return nil, errors.New("kotak returned no live quotes for configured indices")
	}
	return result, nil
}

// Expiries returns the available expiries for a configured index.
func (s *Service) Expiries(indexKey string) ([]scrip.ExpiryInfo, error) {
	if !s.configuredUnderlying(indexKey) {
		return nil, fmt.Errorf("unknown underlying %q", indexKey)
	}
	expiries := s.scrips.Expiries(indexKey)
	if len(expiries) == 0 {
		return nil, fmt.Errorf("no Scrip Master expiries available for %s", indexKey)
	}
	return expiries, nil
}

// Chain fetches option quotes for a configured index and expiry.
func (s *Service) Chain(ctx context.Context, baseURL, indexKey, expiry string) ([]ChainRow, error) {
	page, err := s.ChainWindow(ctx, baseURL, indexKey, expiry, 0, 0, "")
	return page.Rows, err
}

// ChainWindow fetches at most count strikes around center or adjacent to it.
// An empty direction with count=0 preserves the full-chain behavior.
func (s *Service) ChainWindow(ctx context.Context, baseURL, indexKey, expiry string, center float64, count int, direction string) (ChainPage, error) {
	if !s.configuredUnderlying(indexKey) {
		return ChainPage{}, fmt.Errorf("unknown underlying %q", indexKey)
	}
	if !s.validExpiry(indexKey, expiry) {
		return ChainPage{}, fmt.Errorf("expiry %q is unavailable for %s", expiry, indexKey)
	}
	options := s.scrips.Options(indexKey, expiry)
	if len(options) == 0 {
		return ChainPage{}, fmt.Errorf("no option contracts in the Scrip Master for %s %s", indexKey, expiry)
	}
	if count < 0 || count > 20 {
		return ChainPage{}, errors.New("chain strike count must be between 1 and 20")
	}

	allStrikes := make([]int64, 0, len(options)/2)
	seenStrikes := make(map[int64]struct{}, len(options)/2)
	for _, option := range options {
		if _, exists := seenStrikes[option.StrikePaise]; exists {
			continue
		}
		seenStrikes[option.StrikePaise] = struct{}{}
		allStrikes = append(allStrikes, option.StrikePaise)
	}
	sort.Slice(allStrikes, func(i, j int) bool { return allStrikes[i] < allStrikes[j] })
	if count > 0 {
		if direction != "" && direction != "around" && direction != "above" && direction != "below" {
			return ChainPage{}, errors.New("chain direction must be around, above, or below")
		}
		centerPaise := int64(center * 100)
		pivot := sort.Search(len(allStrikes), func(i int) bool { return allStrikes[i] >= centerPaise })
		start, end := 0, 0
		switch direction {
		case "above":
			start = sort.Search(len(allStrikes), func(i int) bool { return allStrikes[i] > centerPaise })
			end = min(start+count, len(allStrikes))
		case "below":
			end = sort.Search(len(allStrikes), func(i int) bool { return allStrikes[i] >= centerPaise })
			start = max(0, end-count)
		default:
			if pivot == len(allStrikes) {
				pivot = len(allStrikes) - 1
			}
			start = max(0, pivot-count/2)
			end = min(start+count, len(allStrikes))
			start = max(0, end-count)
		}
		selectedStrikes := allStrikes[start:end]
		selectedSet := make(map[int64]struct{}, len(selectedStrikes))
		for _, strike := range selectedStrikes {
			selectedSet[strike] = struct{}{}
		}
		filtered := make([]scrip.Option, 0, len(selectedStrikes)*2)
		for _, option := range options {
			if _, ok := selectedSet[option.StrikePaise]; ok {
				filtered = append(filtered, option)
			}
		}
		options = filtered
		return s.chainQuotes(ctx, baseURL, options, start > 0, end < len(allStrikes))
	}
	return s.chainQuotes(ctx, baseURL, options, false, false)
}

func (s *Service) chainQuotes(ctx context.Context, baseURL string, options []scrip.Option, hasMoreAbove, hasMoreBelow bool) (ChainPage, error) {
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
		return ChainPage{}, err
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
	sortStrikeRows(strikes)
	result := make([]ChainRow, 0, len(strikes))
	for _, strike := range strikes {
		result = append(result, *byStrike[strike])
	}
	return ChainPage{Rows: result, HasMoreAbove: hasMoreAbove, HasMoreBelow: hasMoreBelow}, nil
}

// Quotes fetches the latest quotes for many option instruments in as few
// Kotak batch calls as the configured batch size allows. The returned map is
// keyed by instrument key and omits instruments with no live quote.
func (s *Service) Quotes(ctx context.Context, baseURL string, instrumentKeys []string) (map[string]Quote, error) {
	byKey := make(map[string]Quote, len(instrumentKeys))
	if len(instrumentKeys) == 0 {
		return byKey, nil
	}
	instruments := make([]QuoteInstrument, 0, len(instrumentKeys))
	keys := make([]string, 0, len(instrumentKeys))
	for _, key := range instrumentKeys {
		option, ok := s.scrips.Option(key)
		if !ok {
			continue
		}
		instruments = append(instruments, QuoteInstrument{
			ExchangeSegment: option.ExchangeSegment,
			Symbol:          option.PSymbol,
		})
		keys = append(keys, key)
	}
	if len(instruments) == 0 {
		return byKey, nil
	}
	quotes, err := s.fetch(ctx, baseURL, instruments)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		option, ok := s.scrips.Option(key)
		if !ok {
			continue
		}
		if quote, found := quotes[quoteKey(option.ExchangeSegment, option.PSymbol)]; found && quote.LTP > 0 {
			byKey[key] = quote
		}
	}
	return byKey, nil
}

// Quote fetches the latest quote for one Scrip Master instrument.
func (s *Service) Quote(ctx context.Context, baseURL, instrumentKey string) (Quote, error) {
	quotes, err := s.Quotes(ctx, baseURL, []string{instrumentKey})
	if err != nil {
		return Quote{}, err
	}
	quote, ok := quotes[instrumentKey]
	if !ok {
		return Quote{}, fmt.Errorf("kotak returned no live quote for %s", instrumentKey)
	}
	return quote, nil
}

// Candles fetches recent historical candles for an option or configured spot.
func (s *Service) Candles(ctx context.Context, baseURL, instrumentKey, timeframe string) ([]Candle, error) {
	interval, maxDays, ok := historicalTimeframe(timeframe)
	if !ok {
		return nil, fmt.Errorf("unsupported chart timeframe %q", timeframe)
	}
	var segment, symbol string
	if option, found := s.scrips.Option(instrumentKey); found {
		segment, symbol = option.ExchangeSegment, option.PSymbol
	} else {
		spot, found := s.scrips.Spot(instrumentKey)
		if !found {
			spot, found = s.configuredSpot(instrumentKey)
		}
		if !found {
			return nil, fmt.Errorf("unknown market instrument %q", instrumentKey)
		}
		segment, symbol = spot.ExchangeSegment, spot.PSymbol
	}
	if segment == "" || symbol == "" {
		return nil, fmt.Errorf("market instrument %q has no Kotak pSymbol", instrumentKey)
	}
	if segment == scrip.SegmentMCXFO {
		return nil, errors.New("kotak historical candles are not available for MCX contracts")
	}
	days := s.cfg.Charts.RetentionDays
	if days <= 0 || days > maxDays {
		days = maxDays
	}
	now := time.Now().In(time.FixedZone("IST", 5*60*60+30*60))
	// Kotak counts both endpoints in its date-window limit. Request at most
	// `days` calendar dates, including today, rather than `days + 1`.
	fromDate := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	toDate := now.Format("2006-01-02")
	return s.client.Historical(ctx, baseURL, segment+"|"+symbol, interval, fromDate, toDate)
}

func (s *Service) configuredSpot(instrumentKey string) (scrip.Spot, bool) {
	for _, index := range s.cfg.Market.Indices {
		name := strings.TrimSpace(index.ScripName)
		if name == "" {
			name = index.Key
		}
		if instrumentKey != index.IndexSegment+"|"+name && instrumentKey != scrip.SpotKey(index.IndexSegment, name) {
			continue
		}
		if spot, ok := s.scrips.FindSpot(index.IndexSegment, name); ok {
			return spot, true
		}
	}
	return scrip.Spot{}, false
}

func historicalTimeframe(timeframe string) (string, int, bool) {
	switch timeframe {
	case "1m":
		return "1min", 30, true
	case "3m":
		return "3min", 30, true
	case "5m":
		return "5min", 30, true
	case "15m":
		return "15min", 60, true
	case "1h":
		return "60min", 90, true
	default:
		return "", 0, false
	}
}

// Option looks up an instrument in the Scrip Master by its canonical key.
func (s *Service) Option(instrumentKey string) (scrip.Option, bool) {
	return s.scrips.Option(instrumentKey)
}

// FindOption looks up an instrument by exchange segment and broker symbol.
func (s *Service) FindOption(segment, symbol string) (scrip.Option, bool) {
	return s.scrips.FindOption(segment, symbol)
}

// FindSpot resolves a cash index or commodity reference instrument by symbol.
func (s *Service) FindSpot(segment, symbol string) (scrip.Spot, bool) {
	return s.scrips.FindSpot(segment, symbol)
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
	// Kotak returns HTTP 400 for 50-symbol requests on some MCX chain calls,
	// despite documenting a 50-symbol maximum. Keep a margin below that limit.
	if batchSize > 40 {
		batchSize = 40
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

func (s *Service) configuredIndex(key string) bool {
	for _, index := range s.cfg.Market.Indices {
		if index.Key == key {
			return true
		}
	}
	return false
}

func (s *Service) configuredUnderlying(key string) bool {
	if s.configuredIndex(key) {
		return true
	}
	for _, commodity := range s.cfg.Market.Commodities {
		if commodity.Key == key {
			return true
		}
	}
	return false
}

func (s *Service) validExpiry(indexKey, expiry string) bool {
	for _, item := range s.scrips.Expiries(indexKey) {
		if item.Date == expiry && !item.Expired {
			return true
		}
	}
	return false
}

func sortStrikeRows(strikes []int64) {
	for i := 1; i < len(strikes); i++ {
		for j := i; j > 0 && strikes[j] < strikes[j-1]; j-- {
			strikes[j], strikes[j-1] = strikes[j-1], strikes[j]
		}
	}
}
