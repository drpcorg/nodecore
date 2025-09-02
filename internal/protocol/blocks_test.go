package protocol_test

import (
	"testing"

	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestBlockCreation(t *testing.T) {
	block := protocol.NewBlock(uint64(15), uint64(55), "hash", []byte("raw"))
	expectedBlock := protocol.Block{
		BlockData: &protocol.BlockData{Height: uint64(15), Slot: uint64(55), Hash: "hash"},
		RawBlock:  []byte("raw"),
	}

	assert.Equal(t, &expectedBlock, block)
	assert.False(t, block.BlockData.IsEmpty())
}

func TestNewBlockDataWithHeight(t *testing.T) {
	blockData := protocol.NewBlockDataWithHeight(uint64(55))
	expectedBlockData := &protocol.BlockData{Height: uint64(55)}

	assert.Equal(t, expectedBlockData, blockData)
}

func TestBlockData(t *testing.T) {
	tests := []struct {
		name      string
		blockData protocol.BlockData
		expected  bool
	}{
		{
			name:      "empty data",
			blockData: protocol.BlockData{},
			expected:  true,
		},
		{
			name:      "data with height",
			blockData: protocol.BlockData{Height: uint64(12)},
			expected:  false,
		},
		{
			name:      "data with slot",
			blockData: protocol.BlockData{Slot: uint64(12)},
			expected:  false,
		},
		{
			name:      "data with hash",
			blockData: protocol.BlockData{Hash: "hash"},
			expected:  false,
		},
		{
			name:      "data with height and slot",
			blockData: protocol.BlockData{Height: uint64(12), Slot: uint64(1)},
			expected:  false,
		},
		{
			name:      "data with height and hash",
			blockData: protocol.BlockData{Height: uint64(12), Hash: "hash"},
			expected:  false,
		},
		{
			name:      "data with slot and hash",
			blockData: protocol.BlockData{Slot: uint64(12), Hash: "hash"},
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, test.blockData.IsEmpty())
		})
	}
}
