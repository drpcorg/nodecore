package blockchain_test

import (
	"encoding/binary"
	"testing"

	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/stretchr/testify/assert"
)

func TestNewHashIdFromString(t *testing.T) {
	hashStr := "0xbb6e6f14b1656de46de5bdd9f13196e5d556ad071a2ce802bb3e5e704047b40e"
	hashId := blockchain.NewHashIdFromString(hashStr)

	assert.Equal(t, hashStr[2:], hashId.ToHex())
	assert.Equal(t, hashStr, hashId.ToHexWithPrefix())
}

func TestNewHashIdFromBytes(t *testing.T) {
	b1 := make([]byte, 32)
	binary.BigEndian.PutUint64(b1, 405223378)
	hashId := blockchain.NewHashIdFromBytes(b1)

	assert.Equal(t, "00000000182737d2000000000000000000000000000000000000000000000000", hashId.ToHex())
}

func TestNewHashIdFromStringNonHexDecodedAsBase64(t *testing.T) {
	// near ids are base58, ton root hashes are base64 - hex-decoding them
	// used to truncate everything into the same (near-)empty id. Non-hex ids
	// are decoded as base64, matching dshackle's BlockId.fromBase64.
	base58 := blockchain.NewHashIdFromString("9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa")
	base58Other := blockchain.NewHashIdFromString("5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ")
	tonBase64 := blockchain.NewHashIdFromString("m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0=")

	// dshackle parity: hex of the base64-decoded bytes
	assert.Equal(t, "9b640cc67ff51f622a9bedb08c1dde77135afebbcbf55edb53a30c48f9927d6d", tonBase64.ToHex())
	assert.Equal(t, "f6711c1e98dcb1f8ccc07cc76330cb65e2c111b7aa6cd45ec3ba1709216f8b659a", base58.ToHex())
	assert.False(t, base58.Equals(base58Other))
	assert.False(t, base58.Equals(tonBase64))
}

func TestNewHashIdFromStringHexUnchanged(t *testing.T) {
	assert.Equal(t, "abcd", blockchain.NewHashIdFromString("0xabcd").ToHex())
}
