package main

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"time"
)

// convertOne parses a single upstream mapping and converts it.
func convertAll(t *testing.T, yamlText string) ([]*config.Upstream, *report, *section) {
	t.Helper()
	up := mustSection(t, yamlText)
	r := new(report)
	return convertUpstream(up, 0, r), r, up
}

// convertOne is convertAll for the usual case of at most one result.
func convertOne(t *testing.T, yamlText string) (*config.Upstream, bool, *report, *section) {
	t.Helper()
	all, r, up := convertAll(t, yamlText)
	require.LessOrEqual(t, len(all), 1)
	if len(all) == 0 {
		return nil, false, r, up
	}
	return all[0], true, r, up
}

func TestConvertUpstreamFull(t *testing.T) {
	out, ok, r, up := convertOne(t, `
id: eth-1
chain: ETH
role: primary
labels:
  archive: true
  version: 1.10
  empty: ""
custom-headers:
  X-Api-Key: abc
methods:
  enabled:
    - name: parity_trace
    - name: eth_custom
      quorum: not_empty
    - name: net_version
      static: "\"1\""
  disabled:
    - name: admin_shutdown
additional-settings:
  manual-lower-bounds:
    STATE: {type: fixed, value: 100}
    tx: {type: head, value: -1000}
options:
  validate-chain: false
connection:
  generic:
    rpc:
      url: http://localhost:8545
      basic-auth: {username: alice, password: 'p#ss: w"ord'}
      tls: {ca: /ca.crt}
    ws:
      url: ws://localhost:8546
      origin: http://localhost
      bearer-auth: {token: tok}
`)
	require.True(t, ok)
	assert.Equal(t, &config.Upstream{
		Id: "eth-1", ChainName: "ethereum",
		Labels: config.UpstreamLabels{"archive": "true", "version": "1.10"},
		Connectors: []*config.ApiConnectorConfig{
			{Type: "json-rpc", Url: "http://localhost:8545", Ca: "/ca.crt",
				Headers: map[string]string{"X-Api-Key": "abc", "Authorization": "Basic YWxpY2U6cCNzczogdyJvcmQ="}},
			{Type: "websocket", Url: "ws://localhost:8546",
				Headers: map[string]string{"X-Api-Key": "abc", "Authorization": "Bearer tok", "Origin": "http://localhost"}},
		},
		Options:     &chains.Options{DisableChainValidation: new(true)},
		Methods:     &config.MethodsConfig{EnableMethods: []string{"parity_trace", "eth_custom"}, DisableMethods: []string{"admin_shutdown"}},
		LowerBounds: config.UpstreamLowerBounds{"state": 100},
	}, out)
	text := reportText(t, r)
	assert.Contains(t, text, "  methods.enabled[eth_custom].quorum (eth-1): per-method quorum is not supported\n")
	assert.Contains(t, text, "  methods.enabled[net_version].static (eth-1): static responses are not supported; the method was not enabled\n")
	assert.Contains(t, text, "  additional-settings.manual-lower-bounds.tx (eth-1): head-relative bounds are not supported; nodecore bounds are static\n")
	assert.NotContains(t, text, "role")
	assert.Empty(t, up.unread())
}

func TestConvertUpstreamTonSplit(t *testing.T) {
	all, r, up := convertAll(t, `
id: ton-1
chain: ton
labels:
  region: eu
custom-headers:
  X-Api-Key: abc
methods:
  enabled:
    - name: GET#/customMethod
  disabled:
    - name: GET#/getAddressInformation
additional-settings:
  manual-lower-bounds:
    block: {type: fixed, value: 100}
options:
  validation-interval: 20
connection:
  generic:
    rpc:
      url: http://ton-node:8081/api/v2
    additional:
      - tag: ton_v3
        rpc:
          url: http://ton-index:8082
          bearer-auth: {token: tok}
`)
	require.Len(t, all, 2)
	assert.Equal(t, &config.Upstream{
		Id: "ton-1", ChainName: "ton",
		Labels: config.UpstreamLabels{"region": "eu"},
		Connectors: []*config.ApiConnectorConfig{{Type: "rest", Url: "http://ton-node:8081/api/v2",
			Headers: map[string]string{"X-Api-Key": "abc"}}},
		Options:     &chains.Options{ValidationInterval: 20 * time.Second},
		Methods:     &config.MethodsConfig{EnableMethods: []string{"GET#/customMethod"}, DisableMethods: []string{"GET#/getAddressInformation"}},
		LowerBounds: config.UpstreamLowerBounds{"block": 100},
	}, all[0])
	assert.Equal(t, &config.Upstream{
		Id: "ton-1-v3", ChainName: "ton",
		Labels: config.UpstreamLabels{"region": "eu"},
		Connectors: []*config.ApiConnectorConfig{{Type: "rest-indexer", Url: "http://ton-index:8082",
			Headers: map[string]string{"X-Api-Key": "abc", "Authorization": "Bearer tok"}}},
		Options: &chains.Options{ValidationInterval: 20 * time.Second},
		Methods: &config.MethodsConfig{DisableMethods: []string{"GET#/getAddressInformation"}},
	}, all[1])
	assert.NotSame(t, all[0].Options, all[1].Options)
	assert.Contains(t, reportText(t, r), "  connection.generic.additional (ton-1): split into two upstreams as nodecore recommends for TON: ton-1 (v2 API) and ton-1-v3 (v3 indexer); ton-1-v3 is a new upstream id\n")
	assert.Empty(t, up.unread())
}

const (
	placeholderMsg   = "contains a ${VAR} placeholder; nodecore does not substitute environment variables, replace it with the value"
	placeholderFatal = placeholderMsg + " (nodecore will not start until it is replaced)"
	basicPlaceholder = "contains a ${VAR} placeholder; the Authorization header was not written, add `Authorization: Basic <base64(username:password)>` by hand"
	noEquivalent     = "no nodecore equivalent"
	wsOnlyMsg        = "WS_ONLY is not supported when rpc.url is set; nodecore sends requests over HTTP"
	mixedHeadMsg     = "RPC_REQUESTS_WITH_MIXED_HEAD is not supported; the head comes from websocket"
	unknownModeMsg   = "unknown mode; the default was used"
	rpcOnlyWsMsg     = "not carried over: dshackle does not use ws.url in RPC_ONLY mode"
	additionalMsg    = "only the ton_v3 endpoint of a TON upstream is carried over"
	bearerLosesMsg   = "basic-auth is also set and wins, as in dshackle"
	customAuthMsg    = "replaced by the connector's basic-auth or bearer-auth"
	zeromqMsg        = "nodecore has no ZeroMQ head source; the head is polled over rpc"
	clientCertMsg    = "nodecore does not present a client certificate to the node"
	roleMsg          = "roles are not supported; the upstream is treated as primary"
	unknownBoundMsg  = "unknown bound type"
	boundTooSmallMsg = "the value must be 1 or greater"
	basicAliceS3cret = "Basic YWxpY2U6czNjcmV0"
)

// scopedLine is how the report prints an entry of one upstream.
func scopedLine(field, scope, message string) string {
	return "  " + field + " (" + scope + "): " + message + "\n"
}

func connectorTypes(up *config.Upstream) []string {
	types := make([]string, 0, len(up.Connectors))
	for _, connector := range up.Connectors {
		types = append(types, connector.Type)
	}
	return types
}

func TestConvertUpstreamSkips(t *testing.T) {
	cases := []struct{ name, input, scope, reason string }{
		{"no id", "chain: ethereum\nconnection: {generic: {rpc: {url: 'http://n:1'}}}\n", "#0", "no id"},
		{"disabled", "id: a\nenabled: false\nchain: ethereum\nconnection: {generic: {rpc: {url: 'http://n:1'}}}\n", "a",
			"disabled in the dshackle config (enabled: false)"},
		{"grpc", "id: a\nconnection: {grpc: {host: h, port: 2449}}\n", "a",
			"dshackle-to-dshackle upstreams (connection.grpc) are not supported"},
		{"unknown chain", "id: a\nchain: ripple\nconnection: {generic: {rpc: {url: 'http://n:1'}}}\n", "a",
			"chain 'ripple' is not served by nodecore"},
		{"no url", "id: a\nchain: ethereum\nconnection: {generic: {}}\n", "a", "no rpc.url or ws.url"},
		{"no connection", "id: a\nchain: ethereum\n", "a", "no rpc.url or ws.url"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			assert.False(t, ok)
			assert.Nil(t, out)
			assert.Contains(t, reportText(t, r), "skipped upstreams:\n  "+test.scope+": "+test.reason+"\n")
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamConnectorTypes(t *testing.T) {
	generic := func(chain, extra string) string {
		return "id: a\nchain: " + chain + "\nconnection:\n  generic:\n    rpc:\n      url: http://n:1\n" + extra
	}
	cases := []struct {
		name, input string
		types       []string
		drops       []string // fields expected under "not carried over"
		message     string
	}{
		{"evm", generic("ethereum", ""), []string{"json-rpc"}, nil, ""},
		{"beacon", generic("eth-beacon-chain", ""), []string{"rest"}, nil, ""},
		{"algorand", generic("algorand", ""), []string{"rest"}, nil, ""},
		{"cosmos", generic("cosmos-hub", ""), []string{"tendermint"}, nil, ""},
		{"solana with ws", generic("solana", "    ws:\n      url: ws://n:2\n"), []string{"json-rpc", "websocket"}, nil, ""},
		{"ton without indexer", generic("ton", ""), []string{"rest"}, nil, ""},
		{"ton_v3 on a non-TON chain", generic("ethereum", "    additional:\n      - tag: ton_v3\n        rpc:\n          url: http://i:3\n"),
			[]string{"json-rpc"}, []string{"connection.generic.additional"}, additionalMsg},
		{"other tag on TON", generic("ton", "    additional:\n      - tag: other\n        rpc:\n          url: http://i:3\n"),
			[]string{"rest"}, []string{"connection.generic.additional"}, additionalMsg},
		{"bitcoin", "id: a\nchain: bitcoin\nconnection:\n  bitcoin:\n    rpc:\n      url: http://n:1\n    esplora:\n      url: http://e\n    zeromq:\n      address: \"h:1\"\n",
			[]string{"json-rpc", "rest-additional"}, []string{"connection.bitcoin.zeromq"}, zeromqMsg},
		{"esplora on a chain without esplora methods", "id: a\nchain: ethereum\nconnection:\n  bitcoin:\n    rpc:\n      url: http://n:1\n    esplora:\n      url: http://e\n",
			[]string{"json-rpc"}, []string{"connection.bitcoin.esplora"}, "chain 'ethereum' has no esplora methods in nodecore"},
		{"ethereum-pos", "id: a\nchain: ethereum\nconnection:\n  ethereum-pos:\n    upstream-rating: 5\n    execution:\n      rpc:\n        url: http://n:1\n",
			[]string{"json-rpc"}, []string{"connection.ethereum-pos.upstream-rating"}, noEquivalent},
		{"ethereum block", "id: a\nchain: ethereum\nconnection:\n  ethereum:\n    rpc:\n      url: http://n:1\n", []string{"json-rpc"}, nil, ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			require.True(t, ok)
			assert.Equal(t, test.types, connectorTypes(out))
			assert.Equal(t, "http://n:1", out.Connectors[0].Url)
			text := reportText(t, r)
			for _, field := range test.drops {
				assert.Contains(t, text, scopedLine(field, "a", test.message))
			}
			if len(test.drops) == 0 {
				assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", text)
			}
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamConnectorMode(t *testing.T) {
	build := func(rpc, ws bool, mode string) string {
		text := "id: a\nchain: ethereum\nconnection:\n  generic:\n"
		if mode != "" {
			text += "    connector-mode: " + mode + "\n"
		}
		if rpc {
			text += "    rpc:\n      url: http://n:1\n"
		}
		if ws {
			text += "    ws:\n      url: ws://n:2\n"
		}
		return text
	}
	both := []string{"json-rpc", "websocket"}
	cases := []struct {
		name    string
		rpc, ws bool
		mode    string
		types   []string
		field   string
		message string
	}{
		{"default with both", true, true, "", both, "", ""},
		{"default rpc only", true, false, "", []string{"json-rpc"}, "", ""},
		{"default ws only", false, true, "", []string{"websocket"}, "", ""},
		{"RPC_ONLY drops ws", true, true, "RPC_ONLY", []string{"json-rpc"}, "connection.generic.ws", rpcOnlyWsMsg},
		{"WS_ONLY keeps both", true, true, "ws_only", both, "connector-mode", wsOnlyMsg},
		{"mixed head", true, true, "RPC_REQUESTS_WITH_MIXED_HEAD", both, "connector-mode", mixedHeadMsg},
		{"ws head", true, true, "RPC_REQUESTS_WITH_WS_HEAD", both, "", ""},
		{"unknown mode", true, true, "bogus", both, "connector-mode", unknownModeMsg},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, build(test.rpc, test.ws, test.mode))
			require.True(t, ok)
			assert.Equal(t, test.types, connectorTypes(out))
			text := reportText(t, r)
			if test.field == "" {
				assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", text)
			} else {
				assert.Contains(t, text, scopedLine(test.field, "a", test.message))
			}
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamAuthHeaders(t *testing.T) {
	build := func(top, rpcExtra string) string {
		return "id: a\nchain: ethereum\n" + top + "connection:\n  generic:\n    rpc:\n      url: http://n:1\n" + rpcExtra
	}
	basic := "      basic-auth: {username: alice, password: s3cret}\n"
	bearer := "      bearer-auth: {token: tok}\n"
	cases := []struct {
		name, input string
		headers     map[string]string
		field       string
		message     string
	}{
		{"basic", build("", basic), map[string]string{"Authorization": basicAliceS3cret}, "", ""},
		{"bearer", build("", bearer), map[string]string{"Authorization": "Bearer tok"}, "", ""},
		{"basic wins over bearer", build("", basic+bearer), map[string]string{"Authorization": basicAliceS3cret},
			"connection.generic.rpc.bearer-auth", bearerLosesMsg},
		{"auth section replaces a custom header", build("custom-headers: {authorization: X, X-Key: k}\n", basic),
			map[string]string{"Authorization": basicAliceS3cret, "X-Key": "k"}, "custom-headers.Authorization", customAuthMsg},
		{"custom header alone", build("custom-headers: {Authorization: X}\n", ""), map[string]string{"Authorization": "X"}, "", ""},
		{"no headers", build("", ""), nil, "", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			require.True(t, ok)
			assert.Equal(t, test.headers, out.Connectors[0].Headers)
			text := reportText(t, r)
			if test.field == "" {
				assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", text)
			} else {
				assert.Contains(t, text, scopedLine(test.field, "a", test.message))
			}
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamPlaceholders(t *testing.T) {
	build := func(top, url, rpcExtra string) string {
		return "id: a\nchain: ethereum\n" + top + "connection:\n  generic:\n    rpc:\n      url: " + url + "\n" + rpcExtra
	}
	cases := []struct {
		name, input string
		url         string
		headers     map[string]string
		field       string
		message     string
	}{
		{"query", build("", "https://h/x?dkey=${DRPC_KEY}", ""), "https://h/x?dkey=${DRPC_KEY}", nil,
			"connection.generic.rpc.url", placeholderMsg},
		{"host", build("", "http://${HOST}:8545", ""), "http://${HOST}:8545", nil, "connection.generic.rpc.url", placeholderFatal},
		{"whole url", build("", "${NODE_URL}", ""), "${NODE_URL}", nil, "connection.generic.rpc.url", placeholderFatal},
		{"bearer token", build("", "http://n:1", "      bearer-auth: {token: \"${TOKEN}\"}\n"), "http://n:1",
			map[string]string{"Authorization": "Bearer ${TOKEN}"}, "connection.generic.rpc.bearer-auth.token", placeholderMsg},
		{"basic auth", build("", "http://n:1", "      basic-auth: {username: u, password: \"${PASS}\"}\n"), "http://n:1", nil,
			"connection.generic.rpc.basic-auth", basicPlaceholder},
		{"label", build("labels: {region: \"${REGION}\"}\n", "http://n:1", ""), "http://n:1", nil, "labels.region", placeholderMsg},
		{"custom header", build("custom-headers: {X-Key: \"${KEY}\"}\n", "http://n:1", ""), "http://n:1",
			map[string]string{"X-Key": "${KEY}"}, "custom-headers.X-Key", placeholderMsg},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			require.True(t, ok)
			assert.Equal(t, test.url, out.Connectors[0].Url)
			assert.Equal(t, test.headers, out.Connectors[0].Headers)
			text := reportText(t, r)
			assert.Contains(t, text, "needs manual action:\n"+scopedLine(test.field, "a", test.message))
			assert.Empty(t, up.unread())
		})
	}
	out, _, _, _ := convertOne(t, build("labels: {region: \"${REGION}\"}\n", "http://n:1", ""))
	assert.Equal(t, config.UpstreamLabels{"region": "${REGION}"}, out.Labels)
}

func TestConvertUpstreamWarnings(t *testing.T) {
	build := func(top, rpcExtra, connExtra string) string {
		return "id: a\nchain: ethereum\n" + top + "connection:\n  generic:\n    rpc:\n      url: http://n:1\n" + rpcExtra + connExtra
	}
	bounds := func(entry string) string {
		return "additional-settings:\n  manual-lower-bounds:\n    " + entry + "\n"
	}
	cases := []struct {
		name, input string
		fields      []string
		message     string
	}{
		{"node-id", build("node-id: 7\n", "", ""), []string{"node-id"}, "not used by nodecore"},
		{"fallback role", build("role: fallback\n", "", ""), []string{"role"}, roleMsg},
		{"secondary role", build("role: secondary\n", "", ""), []string{"role"}, roleMsg},
		{"client certificate", build("", "      tls: {certificate: /c, key: /k}\n", ""), []string{"connection.generic.rpc.tls"}, clientCertMsg},
		{"max connections", build("", "      max-connections: 10\n", ""), []string{"connection.generic.rpc.max-connections"}, noEquivalent},
		{"queue size", build("", "      queue-size: 10\n", ""), []string{"connection.generic.rpc.queue-size"}, noEquivalent},
		{"ws tuning", build("", "", "    ws: {url: 'ws://n:2', frameSize: 5mb, msgSize: 15mb, connections: 2}\n"),
			[]string{"connection.generic.ws.frameSize", "connection.generic.ws.msgSize", "connection.generic.ws.connections"}, noEquivalent},
		{"unknown bound", build(bounds("bogus: {type: fixed, value: 5}"), "", ""),
			[]string{"additional-settings.manual-lower-bounds.bogus"}, unknownBoundMsg},
		{"bound below one", build(bounds("state: {type: fixed, value: 0}"), "", ""),
			[]string{"additional-settings.manual-lower-bounds.state"}, boundTooSmallMsg},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			require.True(t, ok)
			assert.Nil(t, out.LowerBounds)
			text := reportText(t, r)
			for _, field := range test.fields {
				assert.Contains(t, text, scopedLine(field, "a", test.message))
			}
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamLeavesUnusableValuesUnread(t *testing.T) {
	_, ok, _, up := convertOne(t, `
id: a
chain: ethereum
enabled: "false"
labels: just-a-string
connection:
  generic:
    rpc:
      url: http://n:1
      basic-auth: {username: only-user}
`)
	require.True(t, ok)
	assert.Equal(t, []string{"enabled", "labels", "connection.generic.rpc.basic-auth.username"}, up.unread())
}

func TestHasPlaceholder(t *testing.T) {
	assert.True(t, hasPlaceholder("http://h/?k=${KEY}"))
	assert.True(t, hasPlaceholder("${A}"))
	assert.False(t, hasPlaceholder("http://h/?k=$KEY"))
	assert.False(t, hasPlaceholder("${not a name}"))
	assert.False(t, hasPlaceholder(""))
}

func TestConvertUpstreamDisabledByAnyNonTrueValue(t *testing.T) {
	_, ok, r, _ := convertOne(t, "id: a\nenabled: no\nchain: ethereum\nconnection: {generic: {rpc: {url: 'http://n:1'}}}\n")
	assert.False(t, ok)
	assert.Contains(t, reportText(t, r), "  a: disabled in the dshackle config (enabled: false)\n")
}

func TestConvertUpstreamBitcoinReadsOnlyRpc(t *testing.T) {
	out, ok, _, up := convertOne(t, "id: a\nchain: bitcoin\nconnection:\n  bitcoin:\n    connector-mode: WS_ONLY\n    rpc:\n      url: http://n:1\n    ws:\n      url: ws://n:2\n")
	require.True(t, ok)
	assert.Equal(t, []string{"json-rpc"}, connectorTypes(out))
	assert.Equal(t, []string{"connection.bitcoin.connector-mode", "connection.bitcoin.ws"}, up.unread())
}

// dshackle gives the esplora client its own auth and CA and none of the upstream's custom
// headers.
func TestConvertUpstreamBitcoinEsplora(t *testing.T) {
	out, ok, r, up := convertOne(t, `
id: btc-1
chain: bitcoin
custom-headers:
  X-Api-Key: abc
connection:
  bitcoin:
    rpc:
      url: http://node:8332
    esplora:
      url: https://esplora:3000
      bearer-auth: {token: tok}
      tls: {ca: /etc/ca.pem}
`)
	require.True(t, ok)
	assert.Equal(t, []*config.ApiConnectorConfig{
		{Type: "json-rpc", Url: "http://node:8332", Headers: map[string]string{"X-Api-Key": "abc"}},
		{Type: "rest-additional", Url: "https://esplora:3000", Headers: map[string]string{"Authorization": "Bearer tok"}, Ca: "/etc/ca.pem"},
	}, out.Connectors)
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, r))
	assert.Empty(t, up.unread())
}

// dshackle ignores an esplora block without a url; what it holds must not vanish silently.
func TestConvertUpstreamBitcoinEsploraWithoutUrl(t *testing.T) {
	out, ok, _, up := convertOne(t, "id: a\nchain: bitcoin\nconnection:\n  bitcoin:\n    rpc:\n      url: http://n:1\n    esplora:\n      bearer-auth: {token: tok}\n")
	require.True(t, ok)
	assert.Equal(t, []string{"json-rpc"}, connectorTypes(out))
	assert.Equal(t, []string{"connection.bitcoin.esplora.bearer-auth"}, up.unread())
}

func TestConvertUpstreamGenericWinsOverGrpc(t *testing.T) {
	out, ok, _, up := convertOne(t, "id: a\nchain: ethereum\nconnection:\n  generic:\n    rpc:\n      url: http://n:1\n  grpc:\n    host: h\n")
	require.True(t, ok)
	assert.Equal(t, []string{"json-rpc"}, connectorTypes(out))
	assert.Equal(t, []string{"connection.grpc"}, up.unread())
}

func TestConvertUpstreamModeWithoutItsUrl(t *testing.T) {
	const message = "the mode does not match the configured urls and was ignored"
	cases := map[string]string{
		"RPC_ONLY":                     "    ws:\n      url: ws://n:2\n",
		"WS_ONLY":                      "    rpc:\n      url: http://n:1\n",
		"RPC_REQUESTS_WITH_MIXED_HEAD": "    rpc:\n      url: http://n:1\n",
		"RPC_REQUESTS_WITH_WS_HEAD":    "    rpc:\n      url: http://n:1\n",
	}
	for mode, urls := range cases {
		t.Run(mode, func(t *testing.T) {
			out, ok, r, up := convertOne(t, "id: a\nchain: ethereum\nconnection:\n  generic:\n    connector-mode: "+mode+"\n"+urls)
			require.True(t, ok)
			assert.Len(t, out.Connectors, 1)
			assert.Contains(t, reportText(t, r), scopedLine("connector-mode", "a", message))
			assert.Empty(t, up.unread())
		})
	}
}

func TestConvertUpstreamTonEntryKeepsUnknownKeysVisible(t *testing.T) {
	all, _, up := convertAll(t, `
id: ton-1
chain: ton
connection:
  generic:
    rpc:
      url: http://ton-node:8081/api/v2
    additional:
      - tag: ton_v3
        typo-key: 1
        rpc:
          url: http://ton-index:8082
          typo: 1
`)
	require.Len(t, all, 2)
	assert.Equal(t, []string{
		"connection.generic.additional[0].typo-key",
		"connection.generic.additional[0].rpc.typo",
	}, up.unread())
}

func TestConvertUpstreamUnknownRoleAndBoundKind(t *testing.T) {
	_, ok, r, up := convertOne(t, `
id: a
chain: ethereum
role: bogus
additional-settings:
  manual-lower-bounds:
    state: {type: weird}
connection:
  generic:
    rpc:
      url: http://n:1
`)
	require.True(t, ok)
	text := reportText(t, r)
	assert.Contains(t, text, scopedLine("role", "a", "unknown role 'bogus'; the upstream is treated as primary"))
	assert.Contains(t, text, scopedLine("additional-settings.manual-lower-bounds.state", "a", "unknown bound kind 'weird'"))
	assert.Empty(t, up.unread())
}

func TestConvertUpstreamOwnAuthAlwaysReplacesCustomHeader(t *testing.T) {
	out, ok, r, _ := convertOne(t, `
id: a
chain: ethereum
custom-headers:
  authorization: stale
connection:
  generic:
    rpc:
      url: http://n:1
      basic-auth: {username: u, password: "${PASS}"}
`)
	require.True(t, ok)
	assert.Nil(t, out.Connectors[0].Headers)
	text := reportText(t, r)
	assert.Contains(t, text, scopedLine("custom-headers.Authorization", "a", customAuthMsg))
	assert.Contains(t, text, scopedLine("connection.generic.rpc.basic-auth", "a", basicPlaceholder))
}

// nodecore's methods.enable / methods.disable accept the group names of the chain's method
// spec, so dshackle's method-groups carry over as group names.
func TestConvertUpstreamMethodGroups(t *testing.T) {
	build := func(chain, top string) string {
		return "id: a\nchain: " + chain + "\n" + top + "connection:\n  generic:\n    rpc:\n      url: http://n:1\n"
	}
	cases := []struct {
		name, input string
		methods     *config.MethodsConfig
		lines       []string
	}{
		{"only trace", build("ethereum", "method-groups: {enabled: [trace, debug], disabled: [default]}\n"),
			&config.MethodsConfig{EnableMethods: []string{"trace", "debug"}, DisableMethods: []string{"default"}}, nil},
		{"groups next to methods", build("ethereum", "method-groups: {enabled: [trace]}\nmethods:\n  enabled: [{name: eth_custom}]\n  disabled: [{name: admin_shutdown}]\n"),
			&config.MethodsConfig{EnableMethods: []string{"trace", "eth_custom"}, DisableMethods: []string{"admin_shutdown"}}, nil},
		{"filter can be switched off", build("ethereum", "method-groups: {disabled: [filter]}\n"),
			&config.MethodsConfig{DisableMethods: []string{"filter"}}, nil},
		{"group the chain does not have", build("solana", "method-groups: {enabled: [trace], disabled: [debug]}\n"), nil, []string{
			scopedLine("method-groups.enabled[trace]", "a", "chain 'solana' has no method group 'trace'; not carried over"),
			scopedLine("method-groups.disabled[debug]", "a", "chain 'solana' has no method group 'debug'; not carried over"),
		}},
		{"group in both lists", build("ethereum", "method-groups: {enabled: [trace], disabled: [trace]}\n"),
			&config.MethodsConfig{DisableMethods: []string{"trace"}}, []string{
				scopedLine("method-groups.enabled[trace]", "a", "also listed in disabled; disabled wins, as in dshackle"),
			}},
		{"disabled method inside an enabled group", build("ethereum", "method-groups: {enabled: [trace]}\nmethods:\n  disabled: [{name: trace_call}, {name: admin_shutdown}]\n"),
			&config.MethodsConfig{EnableMethods: []string{"trace"}, DisableMethods: []string{"admin_shutdown"}}, []string{
				scopedLine("methods.disabled[trace_call]", "a", "part of the enabled group 'trace'; nodecore's enable wins, so the method stays on"),
			}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, ok, r, up := convertOne(t, test.input)
			require.True(t, ok)
			assert.Equal(t, test.methods, out.Methods)
			text := reportText(t, r)
			for _, line := range test.lines {
				assert.Contains(t, text, line)
			}
			if len(test.lines) == 0 {
				assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", text)
			}
			assert.Empty(t, up.unread())
		})
	}
}
