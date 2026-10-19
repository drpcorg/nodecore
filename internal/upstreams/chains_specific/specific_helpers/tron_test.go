package specific_helpers_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTronSolidityHeight(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected uint64
		wantErr  bool
	}{
		{"java-tron shape", "Num:75000000,ID:0000000004781f40aabbcc", 75000000, false},
		{"number only", "Num:12", 12, false},
		{"empty", "", 0, true},
		{"missing value", "Num:", 0, true},
		{"id only", "ID:0000000004781f40aabbcc", 0, true},
		{"not a number", "Num:abc,ID:00", 0, true},
		{"negative", "Num:-1,ID:00", 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			height, err := specific_helpers.ParseTronSolidityHeight(test.input)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.expected, height)
		})
	}
}

func TestTronSyncStatus(t *testing.T) {
	const now = int64(1_700_000_000_000)
	tests := []struct {
		name      string
		blockTime int64
		lag       int64
		expected  protocol.AvailabilityStatus
	}{
		{"fresh block", now - 1_000, 5, protocol.Available},
		{"exactly at the lag", now - 5*specific_helpers.TronBlockIntervalMs, 5, protocol.Available},
		{"one block past the lag", now - 6*specific_helpers.TronBlockIntervalMs, 5, protocol.Syncing},
		{"future timestamp (clock skew)", now + 60_000, 5, protocol.Available},
		{"zero lag tolerates one interval minus a ms", now - specific_helpers.TronBlockIntervalMs + 1, 0, protocol.Available},
		{"zero lag, one full interval behind", now - specific_helpers.TronBlockIntervalMs, 0, protocol.Syncing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, specific_helpers.TronSyncStatus(test.blockTime, now, test.lag))
		})
	}
}
