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
