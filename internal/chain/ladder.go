package chain

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ChainBuilder builds option chains from scrip master data.
type ChainBuilder struct {
	// mu protects the chain data.
	mu sync.RWMutex

	// chains holds the built chains for each underlying+expiry combination.
	chains map[string]*Chain

	// spots holds the current spot prices for each underlying.
	spots map[string]*Spot
}

// NewChainBuilder creates a new ChainBuilder.
func NewChainBuilder() *ChainBuilder {
	return &ChainBuilder{
		chains: make(map[string]*Chain),
		spots:  make(map[string]*Spot),
	}
}

// AddOption adds an option to the chain for the given underlying and expiry.
func (cb *ChainBuilder) AddOption(co ChainOption) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	key := chainKey(co.Underlying, co.Expiry)
	chain, exists := cb.chains[key]
	if !exists {
		chain = NewChain(co.Underlying, co.Expiry)
		cb.chains[key] = chain
	}

	chain.AddOption(co)
	return nil
}

// SetSpot sets the spot price for an underlying index.
func (cb *ChainBuilder) SetSpot(underlying UnderlyingID, spot *Spot) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	key := underlyingIDKey(underlying)
	cb.spots[key] = spot
}

// GetChain returns the chain for the given underlying and expiry.
func (cb *ChainBuilder) GetChain(underlying UnderlyingID, expiry Expiry) (*Chain, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	key := chainKey(underlying, expiry)
	chain, exists := cb.chains[key]
	return chain, exists
}

// GetSpot returns the spot price for an underlying index.
func (cb *ChainBuilder) GetSpot(underlying UnderlyingID) (*Spot, bool) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	key := underlyingIDKey(underlying)
	spot, exists := cb.spots[key]
	return spot, exists
}

// Underlyings returns the list of all configured underlying indices.
func (cb *ChainBuilder) Underlyings() []UnderlyingConfig {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var result []UnderlyingConfig
	for key, chain := range cb.chains {
		parts := splitChainKey(key)
		if len(parts) >= 1 {
			underlying, err := parseUnderlyingID(parts[0])
			if err == nil {
				result = append(result, UnderlyingConfig{
					Key:             underlying,
					DisplayName:     underlyingDisplayName(underlying),
					ScripName:       "",
					SpotSymbol:      "",
					ExchangeSegment: "",
					ExpiryDates:     []string{chain.Expiry.String()},
				})
			}
		}
	}
	return result
}

// BuildLadder builds the chain ladder UI data from the chain builder.
func (cb *ChainBuilder) BuildLadder(underlying UnderlyingID, expiry Expiry, spot *Spot) *ChainLadder {
	cb.mu.RLock()
	chain, hasChain := cb.chains[chainKey(underlying, expiry)]
	cb.mu.RUnlock()

	if !hasChain {
		return nil
	}

	return buildLadder(chain, spot)
}

// underlyingIDKey returns a string key for an underlying ID.
func underlyingIDKey(underlying UnderlyingID) string {
	return string(underlying)
}

// chainKey returns a string key for a chain (underlying + expiry).
func chainKey(underlying UnderlyingID, expiry Expiry) string {
	return string(underlying) + ":" + expiry.String()
}

// parseUnderlyingID parses a string into an UnderlyingID.
func parseUnderlyingID(s string) (UnderlyingID, error) {
	switch s {
	case "NIFTY":
		return UnderlyingNIFTY, nil
	case "BANKNIFTY":
		return UnderlyingBANKNIFTY, nil
	case "MIDCPNIFTY":
		return UnderlyingMIDCPNIFTY, nil
	case "SENSEX":
		return UnderlyingSENSEX, nil
	default:
		return "", fmt.Errorf("unknown underlying: %s", s)
	}
}

// underlyingDisplayName returns the display name for an underlying ID.
func underlyingDisplayName(underlying UnderlyingID) string {
	switch underlying {
	case UnderlyingNIFTY:
		return "NIFTY"
	case UnderlyingBANKNIFTY:
		return "BANKNIFTY"
	case UnderlyingMIDCPNIFTY:
		return "MIDCPNIFTY"
	case UnderlyingSENSEX:
		return "SENSEX"
	default:
		return string(underlying)
	}
}

// splitChainKey splits a chain key into parts.
func splitChainKey(key string) []string {
	parts := make([]string, 0, 2)
	for _, part := range strings.Split(key, ":") {
		parts = append(parts, part)
	}
	return parts
}

// buildLadder builds the chain ladder UI data from a Chain.
func buildLadder(c *Chain, spot *Spot) *ChainLadder {
	cl := &ChainLadder{
		Underlying: c.Underlying,
		Expiry:     c.Expiry,
		Spots:      c.Spots,
	}

	// Build chain options sorted by strike
	co := c.BuildIndices()

	// Find ATM strike (closest to spot price)
	// Spot price is in regular price format, strikes are in paise (divide by 100)
	spotPrice := spot.LTP
	if spotPrice <= 0 {
		spotPrice = spot.Close
	}

	var atmStrike Strike
	var minDiff float64
	if spotPrice > 0 {
		minDiff = float64(spotPrice)
		atmStrike = Strike(spotPrice * 100) // approximate
	}

	// Build ladder entries from calls and puts
	allStrikes := make(map[Strike]bool)
	for strike := range co.CallIndex {
		allStrikes[strike] = true
	}
	for strike := range co.PutIndex {
		allStrikes[strike] = true
	}

	// Sort strikes
	var sortedStrikes []Strike
	for strike := range allStrikes {
		sortedStrikes = append(sortedStrikes, strike)
	}
	sort.Slice(sortedStrikes, func(i, j int) bool {
		return sortedStrikes[i] < sortedStrikes[j]
	})

	// Build entries
	for _, strike := range sortedStrikes {
		entry := ChainLadderEntry{
			Strike: strike,
		}

		if call, ok := co.CallIndex[strike]; ok {
			entry.OptionType = Call
			entry.LTP = call.LTP
			entry.PreviousClose = call.PreviousClose
			entry.OI = call.OI
			// Calculate change percentage
			if entry.PreviousClose > 0 {
				entry.Change = (entry.LTP - entry.PreviousClose) / entry.PreviousClose * 100
			}
			// Check if ATM
			if spotPrice > 0 {
				diff := float64(strike)/100.0 - spotPrice
				if diff < 0 {
					diff = -diff
				}
				if diff < minDiff {
					minDiff = diff
					atmStrike = strike
				}
			}
			entry.IsATM = (entry.Strike == atmStrike)
		} else if put, ok := co.PutIndex[strike]; ok {
			entry.OptionType = Put
			entry.LTP = put.LTP
			entry.PreviousClose = put.PreviousClose
			entry.OI = put.OI
			// Calculate change percentage
			if entry.PreviousClose > 0 {
				entry.Change = (entry.LTP - entry.PreviousClose) / entry.PreviousClose * 100
			}
			// Check if ATM
			if spotPrice > 0 {
				diff := float64(strike)/100.0 - spotPrice
				if diff < 0 {
					diff = -diff
				}
				if diff < minDiff {
					minDiff = diff
					atmStrike = strike
				}
			}
			entry.IsATM = (entry.Strike == atmStrike)
		}

		cl.Entries = append(cl.Entries, entry)
	}

	cl.ATMStrike = atmStrike

	return cl
}