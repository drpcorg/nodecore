package eth_validations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestCompatibleClientRulesParsing(t *testing.T) {
	raw := `
rules:
  - client: "client1"
    blacklist:
      - 1.0.0
      - 1.0.1
  - client: "client2"
    blacklist:
      - 2.0.0
      - 2.0.1
`

	var cfg compatibleClientsConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &cfg))
	require.Len(t, cfg.Rules, 2)

	assert.Equal(t, "client1", cfg.Rules[0].Client)
	assert.Equal(t, []string{"1.0.0", "1.0.1"}, cfg.Rules[0].Blacklist)
	assert.Equal(t, "client2", cfg.Rules[1].Client)
	assert.Equal(t, []string{"2.0.0", "2.0.1"}, cfg.Rules[1].Blacklist)
}

func TestCompatibleClientRulesParsingWithNetworks(t *testing.T) {
	raw := `
rules:
  - client: "erigon"
    networks:
      - ethereum
    blacklist:
      - v2.40.0
      - 3.1.0
  - client: "reth"
    blacklist:
      - v1.4.0
      - v1.4.1
  - client: "reth"
    networks:
      - bsc
    blacklist:
      - "reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu"
`

	var cfg compatibleClientsConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &cfg))
	require.Len(t, cfg.Rules, 3)

	assert.Equal(t, "erigon", cfg.Rules[0].Client)
	assert.Equal(t, []string{"ethereum"}, cfg.Rules[0].Networks)
	assert.Equal(t, []string{"v2.40.0", "3.1.0"}, cfg.Rules[0].Blacklist)

	assert.Equal(t, "reth", cfg.Rules[1].Client)
	assert.Nil(t, cfg.Rules[1].Networks)
	assert.Equal(t, []string{"v1.4.0", "v1.4.1"}, cfg.Rules[1].Blacklist)

	assert.Equal(t, "reth", cfg.Rules[2].Client)
	assert.Equal(t, []string{"bsc"}, cfg.Rules[2].Networks)
	assert.Equal(t, []string{"reth/v1.6.0-2a4968e/x86_64-unknown-linux-gnu"}, cfg.Rules[2].Blacklist)
}
