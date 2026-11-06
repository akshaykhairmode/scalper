package webauthn

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// EncodeCBOR encodes basic values into CBOR for tests.
func EncodeCBOR(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeCBORItem(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCBORItem(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case int:
		return encodeInt(buf, int64(val))
	case int32:
		return encodeInt(buf, int64(val))
	case int64:
		return encodeInt(buf, val)
	case uint:
		return encodeHeader(buf, cborMajorUint, uint64(val))
	case uint32:
		return encodeHeader(buf, cborMajorUint, uint64(val))
	case uint64:
		return encodeHeader(buf, cborMajorUint, val)
	case []byte:
		if err := encodeHeader(buf, cborMajorByte, uint64(len(val))); err != nil {
			return err
		}
		buf.Write(val)
		return nil
	case string:
		if err := encodeHeader(buf, cborMajorText, uint64(len(val))); err != nil {
			return err
		}
		buf.WriteString(val)
		return nil
	case []any:
		if err := encodeHeader(buf, cborMajorArr, uint64(len(val))); err != nil {
			return err
		}
		for _, elem := range val {
			if err := encodeCBORItem(buf, elem); err != nil {
				return err
			}
		}
		return nil
	case map[any]any:
		if err := encodeHeader(buf, cborMajorMap, uint64(len(val))); err != nil {
			return err
		}
		for k, elem := range val {
			if err := encodeCBORItem(buf, k); err != nil {
				return err
			}
			if err := encodeCBORItem(buf, elem); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if err := encodeHeader(buf, cborMajorMap, uint64(len(val))); err != nil {
			return err
		}
		for k, elem := range val {
			if err := encodeCBORItem(buf, k); err != nil {
				return err
			}
			if err := encodeCBORItem(buf, elem); err != nil {
				return err
			}
		}
		return nil
	case map[int64]any:
		if err := encodeHeader(buf, cborMajorMap, uint64(len(val))); err != nil {
			return err
		}
		for k, elem := range val {
			if err := encodeCBORItem(buf, k); err != nil {
				return err
			}
			if err := encodeCBORItem(buf, elem); err != nil {
				return err
			}
		}
		return nil
	case bool:
		if val {
			buf.WriteByte((cborMajorSimp << 5) | 21)
		} else {
			buf.WriteByte((cborMajorSimp << 5) | 20)
		}
		return nil
	case nil:
		buf.WriteByte((cborMajorSimp << 5) | 22)
		return nil
	default:
		return fmt.Errorf("cbor: unsupported encode type %T", v)
	}
}

func encodeInt(buf *bytes.Buffer, i int64) error {
	if i >= 0 {
		return encodeHeader(buf, cborMajorUint, uint64(i))
	}
	// Major type 1: -1 - i
	return encodeHeader(buf, cborMajorNint, uint64(-1-i))
}

func encodeHeader(buf *bytes.Buffer, major byte, val uint64) error {
	m := major << 5
	switch {
	case val < 24:
		buf.WriteByte(m | byte(val))
	case val <= 0xff:
		buf.WriteByte(m | 24)
		buf.WriteByte(byte(val))
	case val <= 0xffff:
		buf.WriteByte(m | 25)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(val))
		buf.Write(b[:])
	case val <= 0xffffffff:
		buf.WriteByte(m | 26)
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(val))
		buf.Write(b[:])
	default:
		buf.WriteByte(m | 27)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], val)
		buf.Write(b[:])
	}
	return nil
}
