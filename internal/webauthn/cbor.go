// Package webauthn implements zero-external-dependency WebAuthn (FIDO2) authentication.
package webauthn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// CBOR major types (RFC 8949).
const (
	cborMajorUint = 0
	cborMajorNint = 1
	cborMajorByte = 2
	cborMajorText = 3
	cborMajorArr  = 4
	cborMajorMap  = 5
	cborMajorTag  = 6
	cborMajorSimp = 7
)

// DecodeCBOR decodes the first CBOR data item from b.
func DecodeCBOR(b []byte) (any, error) {
	val, rem, err := decodeCBORItem(b)
	if err != nil {
		return nil, err
	}
	_ = rem
	return val, nil
}

func decodeCBORItem(b []byte) (any, []byte, error) {
	if len(b) == 0 {
		return nil, nil, io.ErrUnexpectedEOF
	}

	header := b[0]
	major := header >> 5
	info := header & 0x1f
	b = b[1:]

	var val uint64
	switch {
	case info < 24:
		val = uint64(info)
	case info == 24:
		if len(b) < 1 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		val = uint64(b[0])
		b = b[1:]
	case info == 25:
		if len(b) < 2 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		val = uint64(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	case info == 26:
		if len(b) < 4 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		val = uint64(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
	case info == 27:
		if len(b) < 8 {
			return nil, nil, io.ErrUnexpectedEOF
		}
		val = binary.BigEndian.Uint64(b[:8])
		b = b[8:]
	default:
		return nil, nil, fmt.Errorf("cbor: unsupported additional info %d", info)
	}

	switch major {
	case cborMajorUint:
		return int64(val), b, nil

	case cborMajorNint:
		// Negative integer: -1 - val
		return -1 - int64(val), b, nil

	case cborMajorByte:
		if uint64(len(b)) < val {
			return nil, nil, io.ErrUnexpectedEOF
		}
		bs := make([]byte, val)
		copy(bs, b[:val])
		return bs, b[val:], nil

	case cborMajorText:
		if uint64(len(b)) < val {
			return nil, nil, io.ErrUnexpectedEOF
		}
		s := string(b[:val])
		return s, b[val:], nil

	case cborMajorArr:
		arr := make([]any, val)
		for i := uint64(0); i < val; i++ {
			elem, rem, err := decodeCBORItem(b)
			if err != nil {
				return nil, nil, err
			}
			arr[i] = elem
			b = rem
		}
		return arr, b, nil

	case cborMajorMap:
		m := make(map[any]any, val)
		for i := uint64(0); i < val; i++ {
			k, rem, err := decodeCBORItem(b)
			if err != nil {
				return nil, nil, err
			}
			b = rem
			v, rem, err := decodeCBORItem(b)
			if err != nil {
				return nil, nil, err
			}
			b = rem
			m[k] = v
		}
		return m, b, nil

	case cborMajorTag:
		// Decode tagged item
		return decodeCBORItem(b)

	case cborMajorSimp:
		switch val {
		case 20:
			return false, b, nil
		case 21:
			return true, b, nil
		case 22, 23:
			return nil, b, nil
		default:
			return nil, b, nil
		}

	default:
		return nil, nil, fmt.Errorf("cbor: unsupported major type %d", major)
	}
}

// Helpers for reading CBOR maps with mixed key types.

func lookupStringKey(m map[any]any, key string) (any, bool) {
	for k, v := range m {
		if s, ok := k.(string); ok && s == key {
			return v, true
		}
	}
	return nil, false
}

func lookupIntKey(m map[any]any, key int64) (any, bool) {
	for k, v := range m {
		switch num := k.(type) {
		case int:
			if int64(num) == key {
				return v, true
			}
		case int64:
			if num == key {
				return v, true
			}
		case uint64:
			if key >= 0 && num == uint64(key) {
				return v, true
			}
		}
	}
	return nil, false
}

func asInt64(v any) (int64, error) {
	switch num := v.(type) {
	case int:
		return int64(num), nil
	case int64:
		return num, nil
	case uint64:
		if num > math.MaxInt64 {
			return 0, errors.New("cbor: integer overflow")
		}
		return int64(num), nil // #nosec G115
	default:
		return 0, errors.New("not an integer")
	}
}
