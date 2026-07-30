package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestUpstreamLabelsParseScalars(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      archive: false
      quoted: "false"
      provider: hetzner
      min-peers: 3
      ratio: 1.5
    connectors:
      - type: json-rpc
        url: https://test.com
`), &cfg)

	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 1)
	assert.Equal(t, UpstreamLabels{
		"archive":   "false",
		"quoted":    "false",
		"provider":  "hetzner",
		"min-peers": "3",
		"ratio":     "1.5",
	}, cfg.Upstreams[0].Labels)
}

func TestUpstreamLabelsParseNullValue(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      archive:
`), &cfg)

	require.NoError(t, err)
	assert.Equal(t, UpstreamLabels{"archive": ""}, cfg.Upstreams[0].Labels)
}

func TestUpstreamLabelsRejectNonScalarValue(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      archive:
        - a
        - b
`), &cfg)

	assert.ErrorContains(t, err, "label 'archive' must have a scalar value")
}

func TestUpstreamLabelsRejectNonMapping(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels: archive
`), &cfg)

	assert.ErrorContains(t, err, "labels must be a mapping of label names to scalar values")
}

func TestUpstreamLabelsRejectDuplicateKey(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      archive: "false"
      archive: "true"
`), &cfg)

	// yaml.v3 does not reject the duplicate mapping key itself, so our own check is
	// always the one that fires here; pin the exact message rather than a substring.
	require.Error(t, err)
	assert.Equal(t, "duplicate label 'archive'", err.Error())
}

func TestUpstreamLabelsAliasToScalarValue(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      provider: &prov hetzner
      backup-provider: *prov
`), &cfg)

	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 1)
	assert.Equal(t, UpstreamLabels{
		"provider":        "hetzner",
		"backup-provider": "hetzner",
	}, cfg.Upstreams[0].Labels)
}

func TestUpstreamLabelsAliasForWholeMapping(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
common-labels: &commonLabels
  archive: "false"
  provider: hetzner
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels: *commonLabels
`), &cfg)

	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 1)
	assert.Equal(t, UpstreamLabels{
		"archive":  "false",
		"provider": "hetzner",
	}, cfg.Upstreams[0].Labels)
}

func TestUpstreamLabelsMergeKeyRejectedWithIntelligibleError(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
common-labels: &commonLabels
  provider: hetzner
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      <<: *commonLabels
      archive: "false"
`), &cfg)

	require.Error(t, err)
	assert.Equal(t, "labels does not support the YAML merge key '<<'; list each label explicitly, or alias the whole labels mapping instead (labels: *shared)", err.Error())
}
