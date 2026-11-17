package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertFullFixture(t *testing.T) {
	input, err := os.ReadFile("testdata/full/dshackle.yaml")
	require.NoError(t, err)
	res, err := convert(input)
	require.NoError(t, err)

	data, err := marshalConfig(res.config)
	require.NoError(t, err)
	want, _ := os.ReadFile("testdata/full/nodecore.yml")
	assert.Equal(t, string(want), string(data))
	strictDecode(t, data)

	var out strings.Builder
	res.report.write(&out, res.converted, res.total, "nodecore.yml")
	wantReport, _ := os.ReadFile("testdata/full/report.txt")
	assert.Equal(t, string(wantReport), out.String())
	assert.Equal(t, 3, res.converted)
	assert.Equal(t, 6, res.total)
}

func TestConvertErrors(t *testing.T) {
	cases := map[string]string{
		"key: [unclosed":                                "",
		"port: 2449\n":                                  "no upstreams found in cluster.upstreams",
		"cluster:\n  upstreams: foo\n":                  "no upstreams found in cluster.upstreams",
		"cluster:\n  upstreams:\n    - just-a-string\n": "no upstreams found in cluster.upstreams",
		"cluster:\n  upstreams:\n    - id: a\n      chain: ripple\n      connection: {generic: {rpc: {url: 'http://x'}}}\n": "no upstream could be converted",
		"cluster:\n  upstreams:\n" +
			"    - {id: eth-1, chain: ethereum, connection: {generic: {rpc: {url: 'http://a'}}}}\n" +
			"    - {id: eth-1, chain: ethereum, connection: {generic: {rpc: {url: 'http://b'}}}}\n": "upstream with id 'eth-1' already exists",
	}
	for input, want := range cases {
		res, err := convert([]byte(input))
		require.Error(t, err, input)
		assert.Contains(t, err.Error(), want)
		if want != "" {
			require.NotNil(t, res)
			require.NotNil(t, res.report)
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "nodecore.yml")
	var stderr strings.Builder

	assert.Equal(t, 0, run([]string{"-in", "testdata/full/dshackle.yaml", "-out", out}, &stderr))
	written, err := os.ReadFile(out)
	require.NoError(t, err)
	want, _ := os.ReadFile("testdata/full/nodecore.yml")
	assert.Equal(t, string(want), string(written))
	assert.Contains(t, stderr.String(), "converted 3 of 6 upstreams -> "+out)
	info, _ := os.Stat(out)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	stderr.Reset()
	assert.Equal(t, 1, run([]string{"-in", "testdata/full/dshackle.yaml", "-out", out}, &stderr))
	assert.Contains(t, stderr.String(), "already exists; pass -force to overwrite it")

	assert.Equal(t, 0, run([]string{"-in", "testdata/full/dshackle.yaml", "-out", out, "-force"}, &stderr))
	assert.Equal(t, 1, run([]string{"-in", filepath.Join(dir, "missing.yaml"), "-out", filepath.Join(dir, "o.yml")}, &stderr))
	assert.Equal(t, 1, run([]string{"-out", filepath.Join(dir, "o.yml")}, &stderr))

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("port: 2449\n"), 0o600))
	failed := filepath.Join(dir, "failed.yml")
	stderr.Reset()
	assert.Equal(t, 1, run([]string{"-in", bad, "-out", failed}, &stderr))
	assert.Contains(t, stderr.String(), "error: no upstreams found in cluster.upstreams")
	assert.NoFileExists(t, failed)
}

// Every connector rule and the TON split must survive nodecore's own validation, not only the
// converter's mapping.
func TestConvertNonEvmUpstreamsPassValidation(t *testing.T) {
	res, err := convert([]byte(`
cluster:
  upstreams:
    - id: ton-1
      chain: ton
      connection:
        generic:
          rpc: {url: "http://ton-node:8081/api/v2"}
          additional:
            - tag: ton_v3
              rpc: {url: "http://ton-index:8082"}
    - id: beacon-1
      chain: eth-beacon-chain
      connection: {generic: {rpc: {url: "http://beacon:5052"}}}
    - id: algo-1
      chain: algorand
      connection: {generic: {rpc: {url: "http://algo:8080"}}}
    - id: cosmos-1
      chain: cosmos-hub
      connection: {generic: {rpc: {url: "http://cosmos:26657"}}}
    - id: sol-1
      chain: solana
      connection: {generic: {rpc: {url: "http://sol:8899"}, ws: {url: "ws://sol:8900"}}}
    - id: btc-1
      chain: bitcoin
      connection: {bitcoin: {rpc: {url: "http://btc:8332"}, esplora: {url: "http://esplora:3000"}}}
`))
	require.NoError(t, err)
	assert.Equal(t, 6, res.converted)
	ids := make([]string, 0)
	for _, upstream := range res.config.UpstreamConfig.Upstreams {
		ids = append(ids, upstream.Id)
	}
	assert.Equal(t, []string{"ton-1", "ton-1-v3", "beacon-1", "algo-1", "cosmos-1", "sol-1", "btc-1"}, ids)
	bitcoin := res.config.UpstreamConfig.Upstreams[6]
	assert.Equal(t, []string{"json-rpc", "rest-additional"}, connectorTypes(bitcoin))
	assert.NotContains(t, reportText(t, res.report), "skipped upstreams")
}

// An upstream's peer options sit on top of the chain defaults. dshackle merges min-peers and
// validate-peers one by one, nodecore resolves them separately, so the upstream must say
// validate-peers itself whenever inheriting the default would give another result.
func TestConvertUpstreamPeerOptionsOverChainDefaults(t *testing.T) {
	cases := []struct {
		name, defaults, upstream string
		want                     *chains.Options
	}{
		{"positive min-peers over a zero default", "{min-peers: 0}", "{min-peers: 5}",
			&chains.Options{MinPeers: 5, ValidatePeers: new(true)}},
		{"positive min-peers over a zero default with the flag on", "{min-peers: 0, validate-peers: true}", "{min-peers: 5}",
			&chains.Options{MinPeers: 5, ValidatePeers: new(true)}},
		{"the default switched the check off by the flag", "{min-peers: 0, validate-peers: false}", "{min-peers: 5}",
			&chains.Options{MinPeers: 5}},
		{"the flag alone does not undo a zero default", "{min-peers: 0}", "{validate-peers: true}",
			&chains.Options{ValidatePeers: new(false)}},
		{"no peer option on the upstream", "{min-peers: 0}", "{validate-syncing: false}",
			&chains.Options{ValidateSyncing: new(false)}},
		{"a positive default", "{min-peers: 3}", "{min-peers: 5}", &chains.Options{MinPeers: 5}},
		{"zero on the upstream", "{min-peers: 3}", "{min-peers: 0}", &chains.Options{ValidatePeers: new(false)}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			res, err := convert([]byte("host: 0.0.0.0\nmonitoring: {enabled: false}\ncluster:\n  defaults:\n    - chains: [ethereum]\n      options: " +
				test.defaults + "\n  upstreams:\n    - id: eth-1\n      chain: ethereum\n      options: " + test.upstream +
				"\n      connection: {generic: {rpc: {url: \"http://n:8545\"}}}\n"))
			require.NoError(t, err)
			require.Len(t, res.config.UpstreamConfig.Upstreams, 1)
			assert.Equal(t, test.want, res.config.UpstreamConfig.Upstreams[0].Options)
			assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, res.report))
		})
	}
}

// Without chain defaults for its chain an upstream's peer options are converted on their own.
func TestConvertUpstreamPeerOptionsWithoutChainDefaults(t *testing.T) {
	res, err := convert([]byte("host: 0.0.0.0\nmonitoring: {enabled: false}\ncluster:\n  defaults:\n    - chains: [solana]\n      options: {min-peers: 0}\n" +
		"  upstreams:\n    - id: eth-1\n      chain: ethereum\n      options: {min-peers: 5}\n      connection: {generic: {rpc: {url: \"http://n:8545\"}}}\n"))
	require.NoError(t, err)
	assert.Equal(t, &chains.Options{MinPeers: 5}, res.config.UpstreamConfig.Upstreams[0].Options)
}

func TestConvertDropsDefaultsNodecoreRejects(t *testing.T) {
	res, err := convert([]byte(`
cluster:
  defaults:
    - chains: [ethereum]
      options: {validation-interval: -5}
    - chains: [solana]
      options: {min-peers: 3}
  upstreams:
    - id: eth-1
      chain: ethereum
      connection: {generic: {rpc: {url: "http://n:1"}}}
    - id: sol-1
      chain: solana
      connection: {generic: {rpc: {url: "http://n:2"}}}
`))
	require.NoError(t, err)
	assert.Equal(t, 2, res.converted)
	assert.NotContains(t, res.config.UpstreamConfig.ChainDefaults, "ethereum")
	assert.Contains(t, res.config.UpstreamConfig.ChainDefaults, "solana")
	text := reportText(t, res.report)
	assert.Contains(t, text, "  chain-defaults.ethereum: rejected by nodecore validation: ")
	assert.NotContains(t, text, "skipped upstreams")
}

func TestRunForceResetsFileMode(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nodecore.yml")
	require.NoError(t, os.WriteFile(out, []byte("old"), 0o644))
	var stderr strings.Builder
	require.Equal(t, 0, run([]string{"-in", "testdata/full/dshackle.yaml", "-out", out, "-force"}, &stderr))
	info, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestConvertMethodGroupsPassValidation(t *testing.T) {
	res, err := convert([]byte(`
cluster:
  upstreams:
    - id: eth-1
      chain: ethereum
      method-groups:
        enabled: [trace, debug]
        disabled: [default]
      connection: {generic: {rpc: {url: "http://n:1"}, ws: {url: "ws://n:2"}}}
`))
	require.NoError(t, err)
	require.Len(t, res.config.UpstreamConfig.Upstreams, 1)
	assert.Equal(t, &config.MethodsConfig{EnableMethods: []string{"trace", "debug"}, DisableMethods: []string{"default"}}, res.config.UpstreamConfig.Upstreams[0].Methods)
	data, err := marshalConfig(res.config)
	require.NoError(t, err)
	parsed := strictDecode(t, data)
	assert.Equal(t, []string{"trace", "debug"}, parsed.UpstreamConfig.Upstreams[0].Methods.EnableMethods)
	assert.Equal(t, []string{"default"}, parsed.UpstreamConfig.Upstreams[0].Methods.DisableMethods)
}

// A section that is switched off in dshackle is written switched off, with its paths, so the
// operator sees it and only has to flip the flag.
func TestConvertKeepsDisabledTlsAndAuth(t *testing.T) {
	res, err := convert([]byte(`
host: 0.0.0.0
tls:
  enabled: false
  server: {certificate: /etc/tls/cert.pem, key: /etc/tls/key.pem}
auth:
  enabled: false
  publicKeyOwner: drpc
  server:
    keys: {provider-private-key: /etc/auth/private.pem, external-public-key: /etc/auth/public.pem}
monitoring: {prometheus: {bind: 0.0.0.0}}
cluster:
  upstreams:
    - id: eth-1
      chain: ethereum
      connection: {generic: {rpc: {url: "http://n:1"}}}
`))
	require.NoError(t, err)
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, res.report))

	data, err := marshalConfig(res.config)
	require.NoError(t, err)
	parsed := strictDecode(t, data)
	assert.False(t, parsed.ServerConfig.TlsConfig.Enabled)
	assert.Equal(t, "/etc/tls/cert.pem", parsed.ServerConfig.TlsConfig.Certificate)
	assert.Equal(t, "/etc/tls/key.pem", parsed.ServerConfig.TlsConfig.Key)
	assert.False(t, parsed.ServerConfig.GrpcAuthConfig.Enabled)
	assert.Equal(t, "drpc", parsed.ServerConfig.GrpcAuthConfig.PublicKeyOwner)
	assert.Equal(t, "/etc/auth/private.pem", parsed.ServerConfig.GrpcAuthConfig.ProviderPrivateKeyPath)
	assert.Equal(t, "/etc/auth/public.pem", parsed.ServerConfig.GrpcAuthConfig.ExternalPublicKeyPath)
	assert.Contains(t, string(data), "  tls:\n    enabled: false\n")
	assert.Contains(t, string(data), "  grpc-auth:\n    enabled: false\n")

	// nodecore's own loader accepts the file as written, with the disabled grpc-auth left in
	// and its key files absent from this machine
	_, err = config.LoadReloadable(data, data)
	require.NoError(t, err)
}

func TestMarshalOmitsMissingPathsOfDisabledSections(t *testing.T) {
	cfg := &config.AppConfig{ServerConfig: &config.ServerConfig{GrpcPort: 2449,
		TlsConfig: &config.TlsConfig{Certificate: "/c"}, GrpcAuthConfig: &config.GrpcAuthConfig{PublicKeyOwner: "drpc"}}}
	data, err := marshalConfig(cfg)
	require.NoError(t, err)
	text := string(data)
	assert.Contains(t, text, "certificate: /c")
	assert.NotContains(t, text, "key:")
	assert.NotContains(t, text, "provider-private-key-path")
	assert.NotContains(t, text, "external-public-key-path")
	strictDecode(t, data)
}
