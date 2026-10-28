package hsm

// Quote contains normalized SFeed market data for an instrument.
type Quote struct {
	InstrumentKey string  `json:"instrument_key"`
	LTP           float64 `json:"ltp"`
	PreviousClose float64 `json:"prev_close"`
	Change        float64 `json:"change"`
	ChangePct     float64 `json:"change_pct"`
	Volume        int64   `json:"volume"`
	Timestamp     int64   `json:"timestamp"`
	OI            int64   `json:"oi"`
	Bid           float64 `json:"bid"`
	Ask           float64 `json:"ask"`
	Identifier    string  `json:"identifier"`
}
