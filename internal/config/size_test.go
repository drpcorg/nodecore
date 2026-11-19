package config

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		size     string
		expected int64
	}{
		{size: "1KB", expected: 1024},
		{size: "512KB", expected: 512 * 1024},
		{size: "1MB", expected: 1024 * 1024},
		{size: "32MB", expected: 32 * 1024 * 1024},
		{size: "32 MB", expected: 32 * 1024 * 1024},
		// the largest sizes that still fit in bytes
		{size: fmt.Sprintf("%dKB", int64(math.MaxInt64/1024)), expected: math.MaxInt64 / 1024 * 1024},
		{size: fmt.Sprintf("%dMB", int64(math.MaxInt64/(1024*1024))), expected: math.MaxInt64 / (1024 * 1024) * (1024 * 1024)},
	}

	for _, test := range tests {
		t.Run(test.size, func(te *testing.T) {
			parsed, err := parseSize(test.size)

			require.NoError(te, err)
			assert.Equal(te, test.expected, parsed)
		})
	}
}

func TestParseSizeWrongSizeThenError(t *testing.T) {
	tests := []struct {
		size     string
		expected string
	}{
		{size: "", expected: "size must be in KB or MB"},
		{size: "100", expected: "size must be in KB or MB"},
		{size: "2GB", expected: "size must be in KB or MB"},
		{size: "1mb", expected: "size must be in KB or MB"},
		{size: "MB", expected: "couldn't parse size"},
		{size: "1.5MB", expected: "couldn't parse size"},
		{size: "tenKB", expected: "couldn't parse size"},
		{size: "0KB", expected: "size must be > 0"},
		{size: "-1MB", expected: "size must be > 0"},
		// a number that fits on its own and no longer does once it is in bytes:
		// unchecked, the multiplication wraps into a small or negative limit
		{size: "99999999999999MB", expected: "size is too large"},
		{size: fmt.Sprintf("%dKB", int64(math.MaxInt64/1024)+1), expected: "size is too large"},
		{size: fmt.Sprintf("%dMB", int64(math.MaxInt64/(1024*1024))+1), expected: "size is too large"},
	}

	for _, test := range tests {
		t.Run(test.size, func(te *testing.T) {
			_, err := parseSize(test.size)

			assert.ErrorContains(te, err, test.expected)
		})
	}
}
