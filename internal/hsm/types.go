package hsm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

type MessageType uint8

const (
	ConnectType     MessageType = 1
	KeepaliveType   MessageType = 2
	ACKType         MessageType = 3
	SubscribeType   MessageType = 4
	UnsubscribeType MessageType = 5
	DataType        MessageType = 6
	SnapshotType    MessageType = 9
	OPCType         MessageType = 10
)

const (
	FieldScrip           uint32 = 1
	FieldUpdateVector    uint32 = 2
	FieldTimestamp       uint32 = 3
	FieldVolume          uint32 = 4
	FieldLTP             uint32 = 5
	FieldClose           uint32 = 21
	FieldOI              uint32 = 22
	FieldMultiplier      uint32 = 23
	FieldPrecision       uint32 = 24
	FieldIndexLTP        uint32 = 2
	FieldIndexClose      uint32 = 3
	FieldDepthMultiplier uint32 = 32
	FieldDepthPrecision  uint32 = 33
	FieldString51        uint32 = 51
	FieldString52        uint32 = 52
	FieldString53        uint32 = 53
	FieldString54        uint32 = 54
)

const trashValue = int32(-1 << 31)

type HSMHeader struct {
	Length     uint16
	Type       uint8
	FieldCount uint8
}

type HSMFrame struct {
	Header  HSMHeader
	Fields  map[uint32][]byte
	Payload []byte
}

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

func ParseFrame(data []byte) (HSMFrame, error) {
	if len(data) < 3 {
		return HSMFrame{}, fmt.Errorf("HSM frame too short: %d bytes", len(data))
	}
	length := int(binary.BigEndian.Uint16(data[:2]))
	if length != len(data)-2 {
		return HSMFrame{}, fmt.Errorf("HSM frame length mismatch: header=%d actual=%d", length, len(data)-2)
	}
	frame := HSMFrame{
		Header: HSMHeader{
			Length: binary.BigEndian.Uint16(data[:2]),
			Type:   data[2],
		},
		Payload: data[3:],
	}
	if MessageType(frame.Header.Type) == DataType {
		return frame, nil
	}
	if len(data) < 4 {
		return HSMFrame{}, errors.New("HSM status frame is missing its field count")
	}
	frame.Header.FieldCount = data[3]
	frame.Fields = make(map[uint32][]byte, frame.Header.FieldCount)
	offset := 4
	for i := uint8(0); i < frame.Header.FieldCount; i++ {
		if offset+3 > len(data) {
			return HSMFrame{}, errors.New("HSM frame has a truncated field header")
		}
		fieldID := uint32(data[offset])
		fieldLength := int(binary.BigEndian.Uint16(data[offset+1 : offset+3]))
		offset += 3
		if offset+fieldLength > len(data) {
			return HSMFrame{}, errors.New("HSM frame has a truncated field value")
		}
		frame.Fields[fieldID] = append([]byte(nil), data[offset:offset+fieldLength]...)
		offset += fieldLength
	}
	if offset != len(data) {
		return HSMFrame{}, fmt.Errorf("HSM frame has %d trailing bytes", len(data)-offset)
	}
	return frame, nil
}

func ExtractLTP(fields map[uint32][]byte) (float64, bool) {
	data, ok := fields[FieldLTP]
	if !ok || len(data) < 4 {
		return 0, false
	}
	raw := int32(binary.BigEndian.Uint32(data))
	if raw == trashValue {
		return 0, false
	}
	multiplier := 1.0
	precision := 0
	if value, ok := fields[FieldMultiplier]; ok && len(value) >= 4 {
		multiplier = float64(binary.BigEndian.Uint32(value))
	}
	if value, ok := fields[FieldPrecision]; ok && len(value) >= 4 {
		precision = int(binary.BigEndian.Uint32(value))
	}
	if multiplier <= 0 || precision < 0 || precision > 9 {
		return 0, false
	}
	price := float64(raw) / (multiplier * math.Pow10(precision))
	return price, !math.IsNaN(price) && !math.IsInf(price, 0)
}
