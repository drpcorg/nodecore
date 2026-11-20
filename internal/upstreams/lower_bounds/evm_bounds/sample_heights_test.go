package evm_bounds

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One height is drawn in each of the five equal parts of the stretch, the ends left out.
func TestSampleHeightsBetweenDrawsOneHeightInEachPart(t *testing.T) {
	const from, to, partSize = int64(1_000), int64(6_001), int64(1_000)

	heights := sampleHeightsBetween(from, to, 77)

	require.Len(t, heights, evmBareHeaderSegmentSamples)
	for part, height := range heights {
		partFrom := from + 1 + int64(part)*partSize
		assert.GreaterOrEqual(t, height, partFrom)
		assert.Less(t, height, partFrom+partSize)
	}
}

// The head seeds the draw: the next head gets other heights.
func TestSampleHeightsBetweenChangeWithHead(t *testing.T) {
	assert.NotEqual(t, sampleHeightsBetween(1, 1_000_000, 500), sampleHeightsBetween(1, 1_000_000, 501))
}

// One head gets the same heights every time: a detection cycle can be replayed.
func TestSampleHeightsBetweenRepeatForOneHead(t *testing.T) {
	assert.Equal(t, sampleHeightsBetween(1, 1_000_000, 500), sampleHeightsBetween(1, 1_000_000, 500))
}

// The heights do not come at one step: bare-looking blocks that come by a rule cannot fall in
// step with them.
func TestSampleHeightsBetweenAreNotEvenlySpaced(t *testing.T) {
	heights := sampleHeightsBetween(1, 1_000_000, 500)

	steps := map[int64]struct{}{}
	for i := 1; i < len(heights); i++ {
		steps[heights[i]-heights[i-1]] = struct{}{}
	}
	assert.Greater(t, len(steps), 1)
}

// A stretch shorter than the number of parts gives every block it has, and none when the two
// ends are neighbours.
func TestSampleHeightsBetweenOnShortStretch(t *testing.T) {
	assert.Equal(t, []int64{11, 12}, sampleHeightsBetween(10, 13, 1))
	assert.Empty(t, sampleHeightsBetween(10, 11, 1))
}
