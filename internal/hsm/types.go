package hsm

// DepthLevel represents a single bid or ask level in market depth.
type DepthLevel struct {
	Price    float64 `json:"price"`
	Quantity int64   `json:"quantity"`
	Orders   int64   `json:"orders"`
}

// Quote contains normalized SFeed market data for an instrument.
type Quote struct {
	InstrumentKey string       `json:"instrument_key"`
	LTP           float64      `json:"ltp"`
	PreviousClose float64      `json:"prev_close"`
	Change        float64      `json:"change"`
	ChangePct     float64      `json:"change_pct"`
	Volume        int64        `json:"volume"`
	Timestamp     int64        `json:"timestamp"`
	OI            int64        `json:"oi"`
	Bid           float64      `json:"bid"`
	Ask           float64      `json:"ask"`
	Bids          []DepthLevel `json:"bids,omitempty"`
	Asks          []DepthLevel `json:"asks,omitempty"`
	Identifier    string       `json:"identifier"`
}
