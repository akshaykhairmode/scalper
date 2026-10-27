package chain

import (
	"fmt"
	"sort"
	"time"
)

// Strike represents an option strike price in paise (integer).
type Strike int64

// OptionType is the option type: Call or Put.
type OptionType string

const (
	Call OptionType = "CE"
	Put  OptionType = "PE"
)

// UnderlyingID is the identifier for an underlying index.
type UnderlyingID string

const (
	// UnderlyingNIFTY is the NIFTY index underlying.
	UnderlyingNIFTY UnderlyingID = "NIFTY"
	// UnderlyingBANKNIFTY is the BANKNIFTY index underlying.
	UnderlyingBANKNIFTY UnderlyingID = "BANKNIFTY"
	// UnderlyingMIDCPNIFTY is the MIDCPNIFTY index underlying.
	UnderlyingMIDCPNIFTY UnderlyingID = "MIDCPNIFTY"
	// UnderlyingSENSEX is the SENSEX index underlying.
	UnderlyingSENSEX UnderlyingID = "SENSEX"
)

// OptionTypeFromString converts a string to OptionType.
func OptionTypeFromString(s string) (OptionType, error) {
	switch s {
	case "CE":
		return Call, nil
	case "PE":
		return Put, nil
	default:
		return "", fmt.Errorf("invalid option type: %s", s)
	}
}

// Spot is an index or volatility instrument in the cash segment.
type Spot struct {
	InstrumentKey   string    `json:"instrument_key"`
	ExchangeSegment string    `json:"exchange_segment"`
	Symbol          string    `json:"symbol"`
	DisplayName     string    `json:"display_name"`
	PSymbol         string    `json:"p_symbol"`
	LTP             float64   `json:"ltp"`
	Close           float64   `json:"close"`
}

// Chain is the full option chain for an underlying index.
type Chain struct {
	Underlying UnderlyingID
	Expiry     Expiry
	Options    []ChainOption
	Spots      []Spot
}

// AddOption adds an option to the chain.
func (c *Chain) AddOption(co ChainOption) {
	c.Options = append(c.Options, co)
}

// ChainOption is an option contract from the chain.
type ChainOption struct {
	Underlying    UnderlyingID
	Expiry        Expiry
	OptionType    OptionType
	Strike        Strike
	PSymbol       string
	LotSize       int64
	ExchangeSegment string

	// Market data fields
	LTP         float64
	PreviousClose float64
	OI          int64
}

// Expiry is an option expiry date.
type Expiry time.Time

// String returns the expiry date in "2006-01-02" format.
func (e Expiry) String() string {
	return time.Time(e).Format("2006-01-02")
}

// ChainOptionSorted is ChainOption with a sorted index for quick lookup.
type ChainOptionSorted struct {
	ChainOption
	// CallIndex maps strike -> ChainOption for CE contracts.
	CallIndex map[Strike]ChainOption
	// PutIndex maps strike -> ChainOption for PE contracts.
	PutIndex map[Strike]ChainOption
}

// BuildIndices builds the call/put index maps for quick lookup.
func (c *Chain) BuildIndices() *ChainOptionSorted {
	result := &ChainOptionSorted{
		ChainOption: ChainOption{
			Underlying: c.Underlying,
			Expiry:     c.Expiry,
		},
		CallIndex: make(map[Strike]ChainOption),
		PutIndex:  make(map[Strike]ChainOption),
	}

	for _, opt := range c.Options {
		if opt.OptionType == Call {
			result.CallIndex[opt.Strike] = opt
		} else if opt.OptionType == Put {
			result.PutIndex[opt.Strike] = opt
		}
	}

	return result
}

// SortStrikes sorts the options by strike price.
func (c *Chain) SortStrikes() {
	sort.Slice(c.Options, func(i, j int) bool {
		return c.Options[i].Strike < c.Options[j].Strike
	})
}

// ChainLadder is the sorted chain data for UI display.
type ChainLadder struct {
	Underlying UnderlyingID
	Expiry     Expiry
	Entries    []ChainLadderEntry
	Spots      []Spot
	ATMStrike  Strike // At-the-money strike
}

// ChainLadderEntry is a row in the chain ladder UI.
type ChainLadderEntry struct {
	Strike      Strike
	OptionType  OptionType
	LTP         float64
	PreviousClose float64
	OI          int64
	Change      float64 // percentage change
	IsATM       bool    // At-The-Money flag
}

// NewChain creates a new Chain for the given underlying and expiry.
func NewChain(underlying UnderlyingID, expiry Expiry) *Chain {
	return &Chain{
		Underlying: underlying,
		Expiry:     expiry,
		Options:    []ChainOption{},
		Spots:      []Spot{},
	}
}

// UnderlyingConfig is the configuration for an underlying index.
type UnderlyingConfig struct {
	Key             UnderlyingID
	DisplayName     string
	ScripName       string
	SpotSymbol      string
	ExchangeSegment string
	ExpiryDates     []string
}