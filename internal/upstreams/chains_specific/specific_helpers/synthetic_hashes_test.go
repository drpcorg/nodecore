package specific_helpers_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/stretchr/testify/assert"
)

// The encoding is the one solana has published since it was introduced: the
// height big-endian in the first 8 bytes of a 32-byte id. Pinned here so the
// move cannot silently change the ids solana emits.
func TestSyntheticHashesEncodesHeightBigEndianInFirstEightBytes(t *testing.T) {
	hash, parentHash := specific_helpers.SyntheticHashes(405220706, 405220705)

	// 405220706 == 0x18272d62
	expected := make([]byte, 32)
	expected[4], expected[5], expected[6], expected[7] = 0x18, 0x27, 0x2d, 0x62
	assert.Equal(t, expected, []byte(hash))

	expectedParent := make([]byte, 32)
	expectedParent[4], expectedParent[5], expectedParent[6], expectedParent[7] = 0x18, 0x27, 0x2d, 0x61
	assert.Equal(t, expectedParent, []byte(parentHash))
}

// Parent linkage is the only property consumers actually rely on.
func TestSyntheticHashesLinkConsecutiveHeights(t *testing.T) {
	firstHash, _ := specific_helpers.SyntheticHashes(100, 99)
	_, secondParentHash := specific_helpers.SyntheticHashes(101, 100)

	assert.Equal(t, firstHash, secondParentHash)
}

func TestSyntheticHashesDistinctPerHeight(t *testing.T) {
	hashA, _ := specific_helpers.SyntheticHashes(1, 0)
	hashB, _ := specific_helpers.SyntheticHashes(2, 1)

	assert.NotEqual(t, hashA, hashB)
}
