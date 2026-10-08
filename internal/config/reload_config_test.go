package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reloadBaseConfig = `
server:
  port: 9090
upstream-config:
  chain-defaults:
    ethereum:
      poll-interval: 30s
  upstreams:
    - id: eth-upstream
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://test.com
`

func TestReloadWatchIntervalDefaultsToDisabled(t *testing.T) {
	appConfig, err := config.ParseAppConfig([]byte(reloadBaseConfig))
	require.NoError(t, err)

	assert.Zero(t, appConfig.UpstreamConfig.Reload.WatchInterval)
}

func TestReloadWatchIntervalTooSmall(t *testing.T) {
	_, err := config.ParseAppConfig([]byte(reloadBaseConfig + "  reload:\n    watch-interval: 10ms\n"))

	assert.ErrorContains(t, err, "error during reload config validation, cause: the watch interval can't be less than 100ms")
}

// A reload re-reads the whole file and compares it with the running config, so
// two loads of the same file must never look like a change.
func TestRestartOnlyChangesSameFile(t *testing.T) {
	file, err := os.ReadFile("configs/upstreams/valid-full-config.yaml")
	require.NoError(t, err)

	running, err := config.ParseAppConfig(file)
	require.NoError(t, err)
	loaded, err := config.ParseAppConfig(file)
	require.NoError(t, err)
	// the running config compiles its score function on the first use
	_, err = running.UpstreamConfig.ScorePolicyConfig.GetScoreFunc()
	require.NoError(t, err)

	assert.Empty(t, running.RestartOnlyChanges(loaded))
}

func TestRestartOnlyChangesIgnoresUpstreamList(t *testing.T) {
	running, err := config.ParseAppConfig([]byte(reloadBaseConfig))
	require.NoError(t, err)
	loaded, err := config.ParseAppConfig([]byte(reloadBaseConfig + `
    - id: polygon-upstream
      chain: polygon
      connectors:
        - type: json-rpc
          url: https://test.com
`))
	require.NoError(t, err)

	assert.Empty(t, running.RestartOnlyChanges(loaded))
}

func TestRestartOnlyChangesNamesTheSections(t *testing.T) {
	running, err := config.ParseAppConfig([]byte(reloadBaseConfig))
	require.NoError(t, err)

	changed := strings.NewReplacer(
		"port: 9090", "port: 9191",
	).Replace(reloadBaseConfig) + "  mode: strict\n  reload:\n    watch-interval: 1s\n"
	loaded, err := config.ParseAppConfig([]byte(changed))
	require.NoError(t, err)

	assert.Equal(
		t,
		[]string{"server", "upstream-config.mode (the dispatch and validate-lag defaults of the chains)", "upstream-config.reload"},
		running.RestartOnlyChanges(loaded),
	)
}

// poll-interval and options of chain-defaults only shape upstreams, and a
// reload applies them by replacing those upstreams: telling the operator to
// restart for them would be wrong.
func TestRestartOnlyChangesIgnoresUpstreamDefaultsOfAChain(t *testing.T) {
	running, err := config.ParseAppConfig([]byte(reloadBaseConfig))
	require.NoError(t, err)

	changed := strings.Replace(reloadBaseConfig, "poll-interval: 30s", "poll-interval: 10s\n      options:\n        validate-peers: true", 1)
	loaded, err := config.ParseAppConfig([]byte(changed))
	require.NoError(t, err)

	assert.Empty(t, running.RestartOnlyChanges(loaded))
	// and the change does reach the upstream
	assert.NotEqual(t, running.UpstreamConfig.Upstreams[0].PollInterval, loaded.UpstreamConfig.Upstreams[0].PollInterval)
}

func TestRestartOnlyChangesNamesTheRoutingSettingsOfAChain(t *testing.T) {
	running, err := config.ParseAppConfig([]byte(reloadBaseConfig))
	require.NoError(t, err)

	changed := strings.Replace(reloadBaseConfig, "poll-interval: 30s", `poll-interval: 30s
      validate-lag: true
      dispatch:
        broadcast: true
    polygon:
      balancing-strategy: base
      local-subscriptions:
        enable: false
      label-balancing:
        order: [full]`, 1)
	loaded, err := config.ParseAppConfig([]byte(changed))
	require.NoError(t, err)

	assert.Equal(
		t,
		[]string{
			"upstream-config.chain-defaults.ethereum.dispatch",
			"upstream-config.chain-defaults.ethereum.validate-lag",
			"upstream-config.chain-defaults.polygon.label-balancing",
			"upstream-config.chain-defaults.polygon.balancing-strategy",
			"upstream-config.chain-defaults.polygon.local-subscriptions",
		},
		running.RestartOnlyChanges(loaded),
	)
	// the same in the other direction: a chain that lost its settings
	assert.Len(t, loaded.RestartOnlyChanges(running), 5)
}
