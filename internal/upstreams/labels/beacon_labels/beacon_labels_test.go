package beacon_labels_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/labels/beacon_labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeaconMappingFunc(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expected  string
		expectErr bool
	}{
		{
			name:     "extracts data.version",
			data:     []byte(`{"data":{"version":"Lighthouse/v8.2.0-120c3c6/x86_64-linux"}}`),
			expected: "Lighthouse/v8.2.0-120c3c6/x86_64-linux",
		},
		{
			name:     "empty version",
			data:     []byte(`{"data":{"version":""}}`),
			expected: "",
		},
		{
			name:      "fails on invalid json",
			data:      []byte(`not json`),
			expectErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := beacon_labels.BeaconMappingFunc(tt.data)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, raw)
		})
	}
}
