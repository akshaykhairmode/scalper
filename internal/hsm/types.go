package hsm

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"log/slog"
)

// HSMHeader is the 2-byte length + type + fieldCount header for HSM frames.
type HSMHeader struct {
	Length  uint16 // 2-byte big-endian length
	Type    uint8  // request/response/data type
	FieldCount uint8 // number of fields following the header
}

// HSMFrame represents a complete HSM frame.
type HSMFrame struct {
	Header  HSMHeader
	Fields  map[uint32][]byte // field ID -> raw field data
	Payload []byte
}

// MessageType represents the type of HSM message.
type MessageType uint8

const (
	// ConnectType is the initial connection type (type 1).
	ConnectType MessageType = 1
	// SubscribeType is for subscribing to topics (type 4).
	SubscribeType MessageType = 4
	// ACKType is the acknowledgment type (type 3).
	ACKType MessageType = 3
	// DataType is market data (type 6).
	DataType MessageType = 6
	// KeepaliveType is the keepalive message (type 2).
	KeepaliveType MessageType = 2
)

// FieldIDs for HSM messages - based on docs/HSM_PROTOCOL.md
const (
	// General field IDs
	FieldScrip         = 1  // Scrip ID
	FieldUpdateVector = 2 // Update vector
	FieldTimestamp     = 3 // Timestamp
	FieldLTP           = 5  // Last Traded Price
	FieldVolume        = 4  // Volume
	FieldClose         = 21 // Close price
	FieldOI            = 22 // Open Interest
	FieldMultiplier    = 23 // Price multiplier
	FieldPrecision     = 24 // Price precision

	// Index field IDs
	FieldIndexLTP   = 2 // Index LTP
	FieldIndexClose = 3 // Index close

	// Depth field IDs
	FieldDepthMultiplier = 32
	FieldDepthPrecision  = 33

	// String fields (51-54)
	FieldString51 = 51
	FieldString52 = 52
	FieldString53 = 53
	FieldString54 = 54
)

// HSMClient is the WebSocket client for Kotak Neo HSM connection.
type HSMClient struct {
	mu sync.Mutex

	// URL of the HSM WebSocket server
	url string

	// Connected indicates if the client is currently connected
	connected bool

	// SessionID is the session token from the connection
	sessionID string

	// OnMessage is called when a market data frame is received
	onMessage func(frame HSMFrame)

	// OnConnect is called when the connection is established
	onConnect func(sessionID string)

	// OnDisconnect is called when the connection is lost
	onDisconnect func()

	// logger for status messages
	logger *slog.Logger

	// wg waits for the background goroutines to finish
	wg sync.WaitGroup

	// stopCh is closed to signal the client to stop
	stopCh chan struct{}

	// connectedCh is closed when the connection is established
	connectedCh chan struct{}

	// instrumentSubscriptions tracks which instruments are subscribed
	instrumentSubscriptions map[string]bool

	// quoteCh is the channel for market data updates
	quoteCh chan<- *Quote
}

// Quote is a market quote received via HSM.
type Quote struct {
	InstrumentKey string  `json:"instrument_key"`
	LTP           float64 `json:"ltp"`
	Volume        int64   `json:"volume"`
	Timestamp     int64   `json:"timestamp"`
	OI            int64   `json:"oi"`
	Bid           float64 `json:"bid"`
	Ask           float64 `json:"ask"`
	Identifier    string  `json:"identifier"` // scrip identifier string
}

// NewHSMClient creates a new HSM client connected to the given URL.
func NewHSMClient(url string, quoteCh chan<- *Quote) *HSMClient {
	return &HSMClient{
		url:                     url,
		connectedCh:             make(chan struct{}, 1),
		instrumentSubscriptions: make(map[string]bool),
		quoteCh:                 quoteCh,
	}
}

// Connect connects to the HSM WebSocket server.
func (c *HSMClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	// In a real implementation, this would establish a WebSocket connection
	// to c.url and handle the HSM protocol handshake.
	// For now, we simulate the connection.

	c.connected = true
	c.logger = slog.Default()

	// Simulate connection establishment
	go c.readLoop()

	close(c.connectedCh)
	return nil
}

// IsConnected returns true if the client is currently connected.
func (c *HSMClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Subscribe subscribes to the given instruments.
func (c *HSMClient) Subscribe(instruments []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, inst := range instruments {
		c.instrumentSubscriptions[inst] = true
		c.logger.Info("subscribing to instrument", "event", "hsm_subscribe", "instrument", inst)
	}

	// In a real implementation, this would send an HSM subscribe frame
	// with the subscribed instrument topics.

	return nil
}

// Unsubscribe unsubscribes from the given instruments.
func (c *HSMClient) Unsubscribe(instruments []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, inst := range instruments {
		delete(c.instrumentSubscriptions, inst)
	}

	// In a real implementation, this would send an HSM unsubscribe frame.

	return nil
}

// readLoop reads frames from the WebSocket connection.
func (c *HSMClient) readLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(30 * time.Second) // keepalive interval
	defer ticker.Stop()

	// Send initial keepalive after connection
	// In real implementation, send keepalive frame

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			// Send keepalive every 30 seconds
			// In real implementation, send HSM keepalive frame type 2
		}
	}
}

// Stop stops the HSM client.
func (c *HSMClient) Stop() {
	close(c.stopCh)
	c.mu.Lock()
	c.connected = false
	c.mu.Unlock()
	c.wg.Wait()
}

// WaitForConnection waits for the connection to be established.
func (c *HSMClient) WaitForConnection() <-chan struct{} {
	return c.connectedCh
}

// ParseFrame parses a raw HSM binary frame into an HSMFrame.
func ParseFrame(data []byte) (HSMFrame, error) {
	if len(data) < 2 {
		return HSMFrame{}, fmt.Errorf("frame too short: %d bytes", len(data))
	}

	header := HSMHeader{
		Length:  binary.BigEndian.Uint16(data[0:2]),
		Type:    data[2],
		FieldCount: data[3],
	}

	frame := HSMFrame{
		Header: header,
		Fields: make(map[uint32][]byte),
	}

	// Parse fields from the payload
	offset := 4 // header is 4 bytes (2 length + 1 type + 1 fieldCount)
	for i := uint8(0); i < header.FieldCount; i++ {
		if offset+2 > len(data) {
			break
		}
		fieldID := binary.BigEndian.Uint32(data[offset : offset+2])
		offset += 2

		// Read field length
		if offset+2 > len(data) {
			break
		}
		fieldLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2

		if offset+fieldLength > len(data) {
			break
		}
		frame.Fields[fieldID] = data[offset : offset+fieldLength]
		offset += fieldLength
	}

	frame.Payload = data
	return frame, nil
}

// ExtractLTP extracts the Last Traded Price from an HSM frame.
func ExtractLTP(fields map[uint32][]byte) (float64, bool) {
	data, ok := fields[FieldLTP]
	if !ok {
		return 0, false
	}

	if len(data) < 4 {
		return 0, false
	}

	// LTP is fixed-point int32: raw / (multiplier × 10^precision)
	raw := int32(binary.BigEndian.Uint32(data))

	// Get multiplier and precision if available
	multiplier := 1.0
	precision := 0
	if m, ok := fields[FieldMultiplier]; ok && len(m) >= 4 {
		multiplier = float64(binary.BigEndian.Uint32(m))
	}
	if p, ok := fields[FieldPrecision]; ok && len(p) >= 4 {
		precision = int(binary.BigEndian.Uint32(p))
	}

	// Convert to float: raw / (multiplier × 10^precision)
	value := float64(raw) / (multiplier * math.Pow10(precision))
	return value, true
}