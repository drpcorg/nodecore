package blockchain

import (
	"encoding/hex"
	"strings"
)

var hexDigits = []byte("0123456789abcdef")

var EmptyHash HashId = nil

type HashId []byte

func NewHashIdFromBytes(value []byte) HashId {
	// just pass bytes as is
	if len(value) >= 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X') {
		value = value[2:]
	}
	return value
}

func NewHashIdFromString(value string) HashId {
	clean := value
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		clean = value[2:]
	}

	if len(clean)%2 != 0 {
		clean = "0" + clean
	}

	bytes, err := hex.DecodeString(clean)
	if err != nil {
		return bytes
	}

	return bytes
}

func (h HashId) ToHex() string {
	hexHash := make([]byte, len(h)*2)

	i := 0
	j := 0
	for i < len(h) {
		b := h[i]
		hexHash[j] = hexDigits[(b&0xF0)>>4]
		j++
		hexHash[j] = hexDigits[b&0x0F]
		j++
		i++
	}

	return string(hexHash)
}

func (h HashId) ToHexWithPrefix() string {
	return "0x" + h.ToHex()
}
