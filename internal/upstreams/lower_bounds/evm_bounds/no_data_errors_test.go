package evm_bounds

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestIsEvmNoDataError(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected bool
	}{
		{name: "plain hint", message: "missing trie node abc", expected: true},
		{name: "plain hint is case-insensitive", message: "Header Not Found", expected: true},
		{name: "historical state with hash", message: "historical state 46ace195ef67fb5fcafb60ad705847c8f3d97e55b1aa1941a9b8057c97e2e702 is not available", expected: true},
		{name: "state with root", message: "state 0xabc123 is not available", expected: true},
		{name: "block number not found", message: "block #12345 not found", expected: true},
		{name: "state at block pruned", message: "state at block #12345 is pruned", expected: true},
		{name: "pattern is case-insensitive", message: "Block #7 Not Found", expected: true},
		{name: "unrelated error", message: "execution reverted", expected: false},
		{name: "empty message", message: "", expected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := protocol.ResponseError{Message: test.message}
			assert.Equal(t, test.expected, isEvmNoDataError(&err))
		})
	}
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isEvmNoDataError(nil))
	})
}
