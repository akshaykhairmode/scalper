package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestEncodeConnectMatchesSDKFrame(t *testing.T) {
	got := encodeConnect(session{token: "T", sid: "S"})
	want := []byte{
		0x00, 0x13, 0x01, 0x03,
		0x01, 0x00, 0x01, 0x54,
		0x02, 0x00, 0x01, 0x53,
		0x03, 0x00, 0x06, 0x4a, 0x53, 0x5f, 0x41, 0x50, 0x49,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("connect frame mismatch:\n got % x\nwant % x", got, want)
	}
}

func TestDisplaySecret(t *testing.T) {
	if got := displaySecret("secret-value", false); got != "[REDACTED]" {
		t.Fatalf("redacted display = %q", got)
	}
	if got := displaySecret("secret-value", true); got != `"secret-value"` {
		t.Fatalf("revealed display = %q", got)
	}
	if got := displaySecret("", true); got != "[empty]" {
		t.Fatalf("empty display = %q", got)
	}
}

func TestEncodeSubscribeFrame(t *testing.T) {
	got := encodeSubscribe("if|nse_cm|Nifty 50")
	packetType, fields, _, err := parseFrame(got)
	if err != nil {
		t.Fatal(err)
	}
	if packetType != 4 {
		t.Fatalf("packet type = %d, want 4", packetType)
	}
	blob := fields[1]
	if len(blob) < 3 || binary.BigEndian.Uint16(blob[:2]) != 1 || string(blob[3:]) != "if|nse_cm|Nifty 50" {
		t.Fatalf("unexpected subscription blob: % x", blob)
	}
	if !bytes.Equal(fields[2], []byte{1}) {
		t.Fatalf("channel = % x, want 01", fields[2])
	}
}

func TestParseAuthenticationResponse(t *testing.T) {
	frame := []byte{0x00, 0x0a, 0x01, 0x02, 0x01, 0x00, 0x01, 'K', 0x02, 0x00, 0x01, 0x05}
	parsed, err := parseStatusFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.packetType != 1 || string(parsed.fields[1]) != "K" {
		t.Fatalf("unexpected connection response: %#v", parsed)
	}
	threshold, err := parseThreshold(parsed.fields[2])
	if err != nil || threshold != 5 {
		t.Fatalf("threshold = %d, err=%v; want 5, nil", threshold, err)
	}
}

func TestParseTextInitializationAndConnectionAck(t *testing.T) {
	kind, code, err := parseTextMessage([]byte(`{"message_code":1119,"exchanges":{"nse_cm":{}}}`))
	if err != nil || kind != "" || code != 1119 {
		t.Fatalf("metadata parsed as kind=%q code=%d err=%v", kind, code, err)
	}
	kind, _, err = parseTextMessage([]byte(`[{"type":"cn"}]`))
	if err != nil || kind != "cn" {
		t.Fatalf("connection ack parsed as kind=%q err=%v", kind, err)
	}
}

func TestParseIndexSnapshotAndUpdate(t *testing.T) {
	name := []byte("if|nse_cm|Nifty 50")
	packet := []byte{'S'}
	packet = binary.BigEndian.AppendUint32(packet, 1)
	packet = append(packet, byte(len(name)))
	packet = append(packet, name...)
	packet = append(packet, 10)
	values := make([]int32, 10)
	values[2] = 2425015
	values[3] = 2420000
	values[8] = 1
	values[9] = 2
	for _, value := range values {
		packet = binary.BigEndian.AppendUint32(packet, uint32(value))
	}
	packet = append(packet, 0)

	payload := []byte{0, 1}
	payload = binary.BigEndian.AppendUint16(payload, uint16(len(packet)))
	payload = append(payload, packet...)
	topics := make(map[uint32]*topicState)
	count, _, err := parseDataFrame(payload, 0, topics, string(name))
	if err != nil || count != 1 {
		t.Fatalf("snapshot count=%d err=%v", count, err)
	}
	ltp, ok := fixedIndexPrice(topics[1].values, 2)
	if !ok || math.Abs(ltp-24250.15) > 0.0001 {
		t.Fatalf("snapshot LTP = %f, valid=%v", ltp, ok)
	}

	updatePacket := []byte{'U'}
	updatePacket = binary.BigEndian.AppendUint32(updatePacket, 1)
	updatePacket = append(updatePacket, 10)
	updated := make([]int32, 10)
	for i := range updated {
		updated[i] = math.MinInt32
	}
	updated[2] = 2426015
	for _, value := range updated {
		updatePacket = binary.BigEndian.AppendUint32(updatePacket, uint32(value))
	}
	updatePayload := []byte{0, 1}
	updatePayload = binary.BigEndian.AppendUint16(updatePayload, uint16(len(updatePacket)))
	updatePayload = append(updatePayload, updatePacket...)
	count, _, err = parseDataFrame(updatePayload, 0, topics, string(name))
	if err != nil || count != 1 {
		t.Fatalf("update count=%d err=%v", count, err)
	}
	ltp, ok = fixedIndexPrice(topics[1].values, 2)
	if !ok || math.Abs(ltp-24260.15) > 0.0001 {
		t.Fatalf("updated LTP = %f, valid=%v", ltp, ok)
	}
}
