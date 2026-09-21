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

	// yaml.v3's own decode reports the duplicate, which is why node.Decode is used
	// instead of walking node.Content: the message carries both line numbers, and
	// errors raised from a custom unmarshaller carry no upstream id to locate them by.
	require.Error(t, err)
	assert.ErrorContains(t, err, `line 7: mapping key "archive" already defined at line 6`)
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

func TestUpstreamLabelsMergeKey(t *testing.T) {
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

	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 1)
	assert.Equal(t, UpstreamLabels{
		"provider": "hetzner",
		"archive":  "false",
	}, cfg.Upstreams[0].Labels)
}

// A quoted "<<" is a label literally named '<<' (tag !!str), not a merge key (tag !!merge),
// and must be kept as an ordinary label rather than treated as a merge directive.
func TestUpstreamLabelsQuotedMergeKeyIsALiteralLabel(t *testing.T) {
	var cfg UpstreamConfig
	err := yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      "<<": literal
`), &cfg)

	require.NoError(t, err)
	assert.Equal(t, UpstreamLabels{"<<": "literal"}, cfg.Upstreams[0].Labels)
}

// The deprecated options.archive flag is translated into the 'archive' label so an
// existing config keeps its override instead of silently losing it to auto-detection.
// Only the upstream-level flag is translated - see the chain-defaults case below.
func TestDeprecatedArchiveOptionTranslatedToLabel(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   UpstreamLabels
	}{
		{
			name: "upstream options.archive false",
			config: `
upstreams:
  - id: eth-upstream
    chain: ethereum
    options:
      archive: false
    connectors:
      - type: json-rpc
        url: https://test.com
`,
			want: UpstreamLabels{"archive": "false"},
		},
		{
			name: "upstream options.archive true",
			config: `
upstreams:
  - id: eth-upstream
    chain: ethereum
    options:
      archive: true
    connectors:
      - type: json-rpc
        url: https://test.com
`,
			want: UpstreamLabels{"archive": "true"},
		},
		{
			// setOptionsDefaults has never merged ArchiveCapability, and the old detector
			// read the upstream-level value only, so chain-defaults options.archive parsed
			// but never took effect. The shim does not invent that inheritance.
			name: "chain-defaults options.archive was never inherited",
			config: `
chain-defaults:
  ethereum:
    options:
      archive: false
upstreams:
  - id: eth-upstream
    chain: ethereum
    connectors:
      - type: json-rpc
        url: https://test.com
`,
			want: nil,
		},
		{
			name: "explicit label wins over the deprecated option",
			config: `
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      archive: "true"
    options:
      archive: false
    connectors:
      - type: json-rpc
        url: https://test.com
`,
			want: UpstreamLabels{"archive": "true"},
		},
		{
			name: "other labels are preserved",
			config: `
upstreams:
  - id: eth-upstream
    chain: ethereum
    labels:
      provider: hetzner
    options:
      archive: false
    connectors:
      - type: json-rpc
        url: https://test.com
`,
			want: UpstreamLabels{"provider": "hetzner", "archive": "false"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			var cfg UpstreamConfig
			require.NoError(te, yaml.Unmarshal([]byte(test.config), &cfg))
			cfg.setDefaults(&GrpcAuthConfig{})

			require.Len(te, cfg.Upstreams, 1)
			assert.Equal(te, test.want, cfg.Upstreams[0].Labels)
		})
	}
}

func TestNoArchiveOptionLeavesLabelsAlone(t *testing.T) {
	var cfg UpstreamConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
upstreams:
  - id: eth-upstream
    chain: ethereum
    connectors:
      - type: json-rpc
        url: https://test.com
`), &cfg))
	cfg.setDefaults(&GrpcAuthConfig{})

	assert.Empty(t, cfg.Upstreams[0].Labels, "no archive option must not synthesise a label")
}

// Map iteration order is random, so validate() sorts the label keys - otherwise the
// reported offender varies between runs on a config with several invalid labels.
func TestLabelValidationErrorIsDeterministic(t *testing.T) {
	upstream := &Upstream{Labels: UpstreamLabels{"zeta": "", "alpha": "", "middle": ""}}

	for range 20 {
		err := upstream.validateLabels()
		require.Error(t, err)
		assert.Equal(t, "label 'alpha' must have a non-empty value", err.Error())
	}
}
