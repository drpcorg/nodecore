package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestUpstreamLowerBoundsParse(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    lower-bounds:
      state: 1
      proof: 7
`), &cfg)

	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 1)
	assert.Equal(t, UpstreamLowerBounds{"state": 1, "proof": 7}, cfg.Upstreams[0].LowerBounds)
}

func TestUpstreamLowerBoundsRejectNonIntegerValue(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    lower-bounds:
      state: abc
`), &cfg)

	var typeErr *yaml.TypeError
	assert.ErrorAs(t, err, &typeErr)
}

func TestUpstreamLowerBoundsValidation(t *testing.T) {
	tests := []struct {
		name   string
		bounds UpstreamLowerBounds
		err    string
	}{
		{name: "all types", bounds: UpstreamLowerBounds{
			"slot": 1, "state": 1, "receipts": 1, "tx": 1, "block": 1,
			"logs": 1, "trace": 1, "proof": 1, "epoch": 1, "blob": 1,
		}},
		{
			name:   "unknown key",
			bounds: UpstreamLowerBounds{"foo": 1},
			err:    "lower-bounds: unknown bound type 'foo', allowed: slot, state, receipts, tx, block, logs, trace, proof, epoch, blob",
		},
		{
			name:   "keys are case-sensitive",
			bounds: UpstreamLowerBounds{"State": 1},
			err:    "lower-bounds: unknown bound type 'State', allowed: slot, state, receipts, tx, block, logs, trace, proof, epoch, blob",
		},
		{
			name:   "unknown bound type is not configurable",
			bounds: UpstreamLowerBounds{"unknown": 1},
			err:    "lower-bounds: unknown bound type 'unknown', allowed: slot, state, receipts, tx, block, logs, trace, proof, epoch, blob",
		},
		{name: "zero", bounds: UpstreamLowerBounds{"state": 0}, err: "lower-bounds: bound 'state' must be >= 1, got 0"},
		{name: "negative", bounds: UpstreamLowerBounds{"tx": -5}, err: "lower-bounds: bound 'tx' must be >= 1, got -5"},
		{
			name:   "first invalid key in sorted order is reported",
			bounds: UpstreamLowerBounds{"tx": 0, "block": 0, "state": 0},
			err:    "lower-bounds: bound 'block' must be >= 1, got 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &Upstream{LowerBounds: tt.bounds}
			for range 20 {
				err := upstream.validateLowerBounds()
				if tt.err == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tt.err)
				}
			}
		})
	}
}
