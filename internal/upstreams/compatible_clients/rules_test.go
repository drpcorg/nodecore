package compatible_clients_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const rethBscFile = `
rules:
  - client: reth
    blacklist: [v1.4.0, v1.4.1, v1.4.2]
  - client: reth
    networks: [bsc]
    blacklist: ["reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu"]
`

const besuFile = `
rules:
  - client: besu
    whitelist: [v24.1.0, v24.1.2]
    blacklist: [v23.10.0]
  - client: besu
    networks: [polygon]
    whitelist: [v24.3.0]
    blacklist: [v24.1.0]
`

func decodeRules(t *testing.T, file string) *compatible_clients.Rules {
	t.Helper()
	var rules compatible_clients.Rules
	require.NoError(t, yaml.Unmarshal([]byte(file), &rules))
	return &rules
}

func testChain(chain chains.Chain, shortNames ...string) *chains.ConfiguredChain {
	return &chains.ConfiguredChain{Chain: chain, ShortNames: shortNames}
}

var (
	ethereum = testChain(chains.ETHEREUM, "ethereum")
	bsc      = testChain(chains.BSC, "bsc")
	polygon  = testChain(chains.POLYGON, "polygon")
)

func TestRulesDefaultOnly(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: reth
    blacklist: [v1.4.0]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.0-abc", ethereum))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.0-abc", bsc))
	assert.Equal(t, compatible_clients.Allowed, rules.Check("reth", "v1.5.0", ethereum))
}

func TestRulesNetworkOnly(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: erigon
    networks: [ethereum]
    blacklist: [v2.40.0, "3.1.0"]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("erigon", "2.40.0", ethereum))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("erigon", "v3.1.0", ethereum))
	assert.Equal(t, compatible_clients.NoRule, rules.Check("erigon", "2.40.0", polygon))
}

func TestRulesNetworkExtendsDefault(t *testing.T) {
	rules := decodeRules(t, rethBscFile)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.0-xyz", bsc))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu", bsc))
	assert.Equal(t, compatible_clients.Allowed, rules.Check("reth", "reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu", ethereum))
	assert.Equal(t, compatible_clients.Allowed, rules.Check("reth", "v1.6.0-2a4968e", bsc))
}

func TestRulesWhitelistMostSpecificWins(t *testing.T) {
	rules := decodeRules(t, besuFile)

	assert.Equal(t, compatible_clients.Allowed, rules.Check("besu", "v24.1.2", ethereum))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v24.2.0", ethereum))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v23.10.0", ethereum))

	assert.Equal(t, compatible_clients.Allowed, rules.Check("besu", "v24.3.0", polygon))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v24.1.2", polygon))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v24.1.0", polygon))
}

func TestRulesDefaultWhitelistUsedWhenNetworkHasNone(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: besu
    whitelist: [v24.1.0]
  - client: besu
    networks: [polygon]
    blacklist: [v24.1.0]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("besu", "v24.1.0", polygon))
	assert.Equal(t, compatible_clients.NotWhitelisted, rules.Check("besu", "v24.1.2", polygon))
}

func TestRulesDuplicateEntriesMerge(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: reth
    blacklist: [v1.4.0]
  - client: reth
    blacklist: [v1.4.1]
  - client: reth
    networks: [bsc]
    blacklist: [v1.5.0]
  - client: reth
    networks: [bsc]
    blacklist: [v1.5.1]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.0", ethereum))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.1", ethereum))
	assert.Equal(t, compatible_clients.Allowed, rules.Check("reth", "v1.5.0", ethereum))
	for _, version := range []string{"v1.4.0", "v1.4.1", "v1.5.0", "v1.5.1"} {
		assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", version, bsc), version)
	}
}

func TestRulesSeveralNetworksOnOneEntry(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: reth
    networks: [bsc, polygon]
    blacklist: [v1.5.0]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.5.0", bsc))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.5.0", polygon))
	assert.Equal(t, compatible_clients.NoRule, rules.Check("reth", "v1.5.0", ethereum))
}

func TestRulesCheckNormalizesClientAndNetwork(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: "Reth "
    networks: [Ethereum]
    blacklist: [v1.4.0]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "1.4.0", ethereum))
	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("Reth", "1.4.0", ethereum))
}

func TestRulesMatchesChainByShortName(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: reth
    networks: [bnb]
    blacklist: [v1.4.0]
`)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "1.4.0", testChain(chains.BSC, "bsc", "bnb")))
}

func TestRulesUnknownClientHasNoRule(t *testing.T) {
	rules := decodeRules(t, rethBscFile)

	assert.Equal(t, compatible_clients.NoRule, rules.Check("geth", "v1.4.0", ethereum))
}

func TestRulesUnknownKeysIgnored(t *testing.T) {
	rules := decodeRules(t, `
rules:
  - client: caplin
    networks: [eth-beacon-chain]
    excluded-methods: ["GET#/eth/v1/beacon/headers"]
    blacklist: []
`)

	assert.Equal(t, compatible_clients.Allowed, rules.Check("caplin", "v3.0.0", testChain(chains.ETH_BEACON_CHAIN, "eth-beacon-chain")))
}

func TestRulesEmptyFileHasNoRules(t *testing.T) {
	for _, file := range []string{"rules: []", "{}"} {
		rules := decodeRules(t, file)

		assert.Equal(t, compatible_clients.NoRule, rules.Check("reth", "v1.4.0", ethereum), file)
	}
}

func TestEmbeddedRulesDecode(t *testing.T) {
	rules, err := compatible_clients.Embedded()
	require.NoError(t, err)

	assert.Equal(t, compatible_clients.Blacklisted, rules.Check("reth", "v1.4.0-5e4e5c6", ethereum))
	assert.Equal(t, compatible_clients.Allowed, rules.Check("reth", "v1.5.0-5e4e5c6", ethereum))
}
