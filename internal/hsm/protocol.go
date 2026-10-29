package hsm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	sfeedHeaderSize        = 9
	sfeedIndexCode         = 7207
	sfeedMarketPictureCode = 7208
	sfeedMarketStatus      = 105
	sfeedMarketOpen        = 6511
	sfeedMarketClose       = 6521
	sfeedCASChange         = 104
	sfeedMiniTouchLevel    = 1
	sfeedTouchLevel        = 4
	sfeedDepthLevel        = 8
	sfeedFullDepthLevel    = 16
	defaultPriceDivider    = 100
)

var exchangeNames = map[int8]string{
	0:  "none",
	1:  "nse_cm",
	2:  "nse_fo",
	3:  "cde_fo",
	4:  "nse_com",
	5:  "bse_cm",
	6:  "bse_fo",
	7:  "bse_cd",
	8:  "bse_co",
	9:  "mcx_fo",
	10: "ncd_co",
}

type sfeedPacket struct {
	length      int
	messageCode uint16
	exchangeID  int8
	level       uint8
	auction     uint8
}

func splitSFeedBatch(frame []byte) ([][]byte, error) {
	var packets [][]byte
	for offset := 0; offset < len(frame); {
		if len(frame)-offset < 2 {
			return nil, errors.New("SFeed batch has a truncated packet length")
		}
		size := int(binary.LittleEndian.Uint16(frame[offset : offset+2]))
		if size < sfeedHeaderSize || size > len(frame)-offset {
			return nil, errors.New("SFeed batch has an invalid or truncated packet")
		}
		packets = append(packets, frame[offset:offset+size])
		offset += size
	}
	return packets, nil
}

func decodeSFeedPacket(packet []byte, dividers map[int8]int, tradingSymbols map[string]string) (*Quote, error) {
	header, err := parseSFeedHeader(packet)
	if err != nil {
		return nil, err
	}
	exchange := exchangeName(header.exchangeID)
	divider := dividers[header.exchangeID]
	if divider <= 0 {
		divider = defaultPriceDivider
	}

	switch {
	case header.messageCode == sfeedIndexCode:
		return decodeSFeedIndex(packet, exchange, divider, tradingSymbols)
	case header.messageCode == sfeedMarketOpen, header.messageCode == sfeedMarketClose,
		header.messageCode == sfeedMarketStatus, header.messageCode == sfeedCASChange:
		return nil, nil
	case header.level == sfeedMiniTouchLevel:
		return decodeSFeedMiniTouch(packet, exchange, divider, tradingSymbols)
	case header.level == 2 || header.level == sfeedTouchLevel ||
		header.level == sfeedDepthLevel || header.level == sfeedFullDepthLevel:
		return decodeSFeedMarketPicture(packet, header, exchange, divider, tradingSymbols)
	default:
		return nil, nil
	}
}

func decodeSFeedMiniTouch(packet []byte, exchange string, divider int, symbols map[string]string) (*Quote, error) {
	const miniBodySize = 45
	if len(packet) < sfeedHeaderSize+miniBodySize {
		return nil, errors.New("SFeed mini-touch packet has a truncated body")
	}
	body := packet[sfeedHeaderSize:]
	token := binary.LittleEndian.Uint32(body[0:4])
	lastTradeTime := clampUint64ToInt64(binary.LittleEndian.Uint64(body[4:12]))
	lastTradedPrice := binary.LittleEndian.Uint32(body[12:16])
	closePrice := binary.LittleEndian.Uint32(body[24:28])
	netChangePercent := signedUint32(binary.LittleEndian.Uint32(body[28:32]))
	netChange := signedUint32(binary.LittleEndian.Uint32(body[32:36]))
	tokenText := strconv.FormatUint(uint64(token), 10)
	symbolKey := exchange + "|" + tokenText
	return &Quote{
		InstrumentKey: "sf|" + symbolKey,
		LTP:           float64(lastTradedPrice) / float64(divider),
		PreviousClose: float64(closePrice) / float64(divider),
		Change:        float64(netChange) / float64(divider),
		ChangePct:     float64(netChangePercent) / 100,
		Timestamp:     lastTradeTime,
		Identifier:    symbols[symbolKey],
	}, nil
}

func parseSFeedHeader(packet []byte) (sfeedPacket, error) {
	if len(packet) < sfeedHeaderSize {
		return sfeedPacket{}, fmt.Errorf("SFeed packet is shorter than its %d-byte header", sfeedHeaderSize)
	}
	length := int(binary.LittleEndian.Uint16(packet[:2]))
	if length != len(packet) {
		return sfeedPacket{}, fmt.Errorf("SFeed packet length mismatch: header=%d actual=%d", length, len(packet))
	}
	if packet[4] > 127 {
		return sfeedPacket{}, errors.New("SFeed packet has an invalid exchange ID")
	}
	return sfeedPacket{
		length:      length,
		messageCode: binary.LittleEndian.Uint16(packet[2:4]),
		exchangeID:  int8(packet[4] & 0x7f),
		level:       packet[5],
		auction:     packet[6],
	}, nil
}

func decodeSFeedIndex(packet []byte, exchange string, divider int, symbols map[string]string) (*Quote, error) {
	const indexBodySize = 78
	if len(packet) < sfeedHeaderSize+indexBodySize {
		return nil, errors.New("SFeed index packet has a truncated body")
	}
	body := packet[sfeedHeaderSize:]
	token := binary.LittleEndian.Uint32(body[0:4])
	closePrice := signedUint32(binary.LittleEndian.Uint32(body[8:12]))
	value := signedUint32(binary.LittleEndian.Uint32(body[20:24]))
	lastTradeTime := binary.LittleEndian.Uint64(body[24:32])
	netChangePercent := signedUint32(binary.LittleEndian.Uint32(body[40:44]))
	name := decodeSFeedString(body[57:78])
	if name == "" {
		name = strconv.FormatUint(uint64(token), 10)
	}
	ltp := float64(value) / float64(divider)
	prevClose := float64(closePrice) / float64(divider)
	change := ltp - prevClose
	key := exchange + "|" + name
	return &Quote{
		InstrumentKey: "if|" + key,
		LTP:           ltp,
		PreviousClose: prevClose,
		Change:        change,
		ChangePct:     float64(netChangePercent) / 100,
		Timestamp:     clampUint64ToInt64(lastTradeTime),
		Identifier:    symbols[key],
	}, nil
}

func decodeSFeedMarketPicture(packet []byte, header sfeedPacket, exchange string, divider int, symbols map[string]string) (*Quote, error) {
	const (
		fixedBodyEnd = 144
		depthRowSize = 16
	)
	if len(packet) < fixedBodyEnd {
		return nil, errors.New("SFeed market-picture packet has a truncated fixed body")
	}
	token := binary.LittleEndian.Uint32(packet[9:13])
	volume := clampUint64ToInt64(binary.LittleEndian.Uint64(packet[29:37]))
	lastTradeTime := clampUint64ToInt64(binary.LittleEndian.Uint64(packet[37:45]))
	closePrice := binary.LittleEndian.Uint32(packet[57:61])
	lastTradedPrice := binary.LittleEndian.Uint32(packet[69:73])
	buyDepthCount := binary.LittleEndian.Uint32(packet[89:93])
	sellDepthCount := binary.LittleEndian.Uint32(packet[93:97])
	netChangePercent := signedUint32(binary.LittleEndian.Uint32(packet[99:103]))
	openInterest := clampUint64ToInt64(binary.LittleEndian.Uint64(packet[103:111]))
	netChange := signedUint32(binary.LittleEndian.Uint32(packet[115:119]))
	buyCount, sellCount := int(buyDepthCount), int(sellDepthCount)
	if header.level == sfeedTouchLevel {
		buyCount, sellCount = 1, 1
	}
	rowCount := buyCount + sellCount
	availableRows := (len(packet) - fixedBodyEnd) / depthRowSize
	if rowCount > availableRows {
		rowCount = availableRows
		if buyCount > rowCount {
			buyCount = rowCount
		}
	}
	var bid, ask float64
	var bids, asks []DepthLevel
	for i := 0; i < rowCount; i++ {
		offset := fixedBodyEnd + i*depthRowSize
		qty := clampUint64ToInt64(binary.LittleEndian.Uint64(packet[offset : offset+8]))
		price := signedUint32(binary.LittleEndian.Uint32(packet[offset+8 : offset+12]))
		orders := int64(binary.LittleEndian.Uint32(packet[offset+12 : offset+16]))
		px := float64(price) / float64(divider)
		level := DepthLevel{
			Price:    px,
			Quantity: qty,
			Orders:   orders,
		}
		if i < buyCount {
			if i == 0 {
				bid = px
			}
			if px > 0 || qty > 0 {
				bids = append(bids, level)
			}
		} else {
			if i == buyCount {
				ask = px
			}
			if px > 0 || qty > 0 {
				asks = append(asks, level)
			}
		}
	}

	tokenText := strconv.FormatUint(uint64(token), 10)
	symbolKey := exchange + "|" + tokenText
	ltp := float64(lastTradedPrice) / float64(divider)
	prevClose := float64(closePrice) / float64(divider)
	return &Quote{
		InstrumentKey: "sf|" + symbolKey,
		LTP:           ltp,
		PreviousClose: prevClose,
		Change:        float64(netChange) / float64(divider),
		ChangePct:     float64(netChangePercent) / 100,
		Volume:        volume,
		Timestamp:     lastTradeTime,
		OI:            openInterest,
		Bid:           bid,
		Ask:           ask,
		Bids:          bids,
		Asks:          asks,
		Identifier:    symbols[symbolKey],
	}, nil
}

func exchangeName(id int8) string {
	if name, ok := exchangeNames[id]; ok {
		return name
	}
	return strconv.Itoa(int(id))
}

func decodeSFeedString(raw []byte) string {
	for i, value := range raw {
		if value == 0 {
			raw = raw[:i]
			break
		}
	}
	return strings.TrimSpace(string(raw))
}

func clampUint64ToInt64(value uint64) int64 {
	const maxInt64Uint = uint64(math.MaxInt64)
	if value > maxInt64Uint {
		return math.MaxInt64
	}
	return int64(value)
}

func signedUint32(value uint32) int32 {
	const maxInt32 = uint32(1<<31 - 1)
	if value <= maxInt32 {
		return int32(value)
	}
	return ^int32(^value) // #nosec G115 -- The complement is bounded to MaxInt32.
}
