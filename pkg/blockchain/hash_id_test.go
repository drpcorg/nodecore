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

func TestNewHashIdFromStringNonHexKeptVerbatim(t *testing.T) {
	// near ids are base58, ton root hashes are base64 - hex-decoding them
	// used to truncate everything into the same (near-)empty id
	base58 := blockchain.NewHashIdFromString("9nEcHpjcsfjMwHzHYzDLZeLBEbeqbNRew7oXCSFvi2Wa")
	base58Other := blockchain.NewHashIdFromString("5qJoxdRBSDaZmGuLzjWfDGnWqCvHdRPuJqkyZv7QwXvJ")
	base64Id := blockchain.NewHashIdFromString("m2QMxn/1H2Iqm+2wjB3edxNa/rvL9V7bU6MMSPmSfW0=")

	assert.NotEmpty(t, base58)
	assert.NotEmpty(t, base64Id)
	assert.False(t, base58.Equals(base58Other))
	assert.False(t, base58.Equals(base64Id))
}

func TestNewHashIdFromStringHexUnchanged(t *testing.T) {
	assert.Equal(t, "abcd", blockchain.NewHashIdFromString("0xabcd").ToHex())
}
