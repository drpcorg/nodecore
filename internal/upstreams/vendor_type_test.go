package upstreams_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/stretchr/testify/assert"
)

func TestUpstreamVendorNoUrlsThenUnknown(t *testing.T) {
	vendor := upstreams.DetectUpstreamVendor([]string{})
	assert.Equal(t, upstreams.Unknown, vendor)
}

func TestUpstreamVendorFirstUnknownThenUnknown(t *testing.T) {
	vendor := upstreams.DetectUpstreamVendor([]string{"https://test.com", "https://mainnet.infura.io/v3/123"})
	assert.Equal(t, upstreams.Unknown, vendor)
}

func TestUpstreamVendor(t *testing.T) {
	tests := []struct {
		name     string
		urls     []string
		expected upstreams.UpstreamVendor
	}{
		{
			name:     "infura",
			urls:     []string{"https://mainnet.infura.io/v3/123"},
			expected: upstreams.Infura,
		},
		{
			name:     "alchemy",
			urls:     []string{"https://eth-mainnet.g.alchemy.com/v2"},
			expected: upstreams.Alchemy,
		},
		{
			name:     "alchemy io",
			urls:     []string{"https://eth-mainnet.g.alchemyapi.io/v2/key"},
			expected: upstreams.Alchemy,
		},
		{
			name:     "quicknode",
			urls:     []string{"https://test.blast-mainnet.quiknode.pro/key"},
			expected: upstreams.QuickNode,
		},
		{
			name:     "drpc org",
			urls:     []string{"https://eth.drpc.org/"},
			expected: upstreams.DRPC,
		},
		{
			name:     "drpc live",
			urls:     []string{"https://eth.drpc.live/"},
			expected: upstreams.DRPC,
		},
		{
			name:     "unknown",
			urls:     []string{"https://test.com"},
			expected: upstreams.Unknown,
		},
		{
			name:     "all should be infura",
			urls:     []string{"https://mainnet.infura.io/v3/123", "wss://mainnet.infura.io/v3/123"},
			expected: upstreams.Infura,
		},
		{
			name:     "all are different",
			urls:     []string{"https://mainnet.infura.io/v3/123", "wss://mainnet.infura.io/v3/123", "https://eth.drpc.live/"},
			expected: upstreams.Unknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			vendor := upstreams.DetectUpstreamVendor(test.urls)

			assert.Equal(t, test.expected, vendor)
		})
	}
}
