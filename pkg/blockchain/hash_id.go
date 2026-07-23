package blockchain

import (
	"bytes"
	"encoding/base64"
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
	if err == nil {
		return bytes
	}

	// Non-hex block ids (ton's base64, near's base58) are decoded as base64,
	// matching dshackle's BlockId.fromBase64 so both emit the same id for the
	// same block. Hex-decoding them used to collapse every such id into the
	// same truncated prefix, making distinct blocks (and a block and its
	// parent) indistinguishable.
	if decoded, b64Err := base64.StdEncoding.DecodeString(value); b64Err == nil {
		return decoded
	}
	if decoded, b64Err := base64.RawStdEncoding.DecodeString(value); b64Err == nil {
		return decoded
	}

	// last resort for ids in any other encoding: keep them verbatim so
	// distinct ids stay distinct
	return []byte(value)
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

func (h HashId) String() string {
	return h.ToHex()
}

func (h HashId) ToHexWithPrefix() string {
	return "0x" + h.ToHex()
}

func (h HashId) Equals(hash HashId) bool {
	return bytes.Equal(h, hash)
}
