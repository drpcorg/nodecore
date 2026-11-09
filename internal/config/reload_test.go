package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readReloadFixture(t *testing.T, name string) []byte {
	t.Helper()
	file, err := os.ReadFile(filepath.Join("configs", "reload", name))
	require.NoError(t, err)
	return file
}

func upstreamById(t *testing.T, cfg *config.AppConfig, id string) *config.Upstream {
	t.Helper()
	for _, up := range cfg.UpstreamConfig.Upstreams {
		if up.Id == id {
			return up
		}
	}
	return nil
}

func TestConfigReloadDefaultsToSignalOnly(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/reload/startup.yaml")
	cfg, err := config.NewAppConfig()
	require.NoError(t, err)

	require.NotNil(t, cfg.ConfigReload)
	assert.Equal(t, time.Duration(0), cfg.ConfigReload.WatchInterval)
}

func TestConfigReloadRejectsNegativeWatchInterval(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/reload/negative-watch-interval.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "watch-interval can't be less than 0")
}

func TestAppConfigKeepsItsSource(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/reload/startup.yaml")
	cfg, err := config.NewAppConfig()
	require.NoError(t, err)

	assert.Equal(t, readReloadFixture(t, "startup.yaml"), cfg.Source())
}

func TestConfigPathDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "")
	assert.Equal(t, config.DefaultConfigPath, config.ConfigPath())
	t.Setenv(config.ConfigPathVar, "/etc/nodecore.yml")
	assert.Equal(t, "/etc/nodecore.yml", config.ConfigPath())
}

func TestLoadReloadableTakesOnlyUpstreamsAndChainDefaults(t *testing.T) {
	merged, err := config.LoadReloadable(readReloadFixture(t, "startup.yaml"), readReloadFixture(t, "upstreams-changed.yaml"))
	require.NoError(t, err)

	// restart-only sections keep their startup values
	assert.Equal(t, 9095, merged.ServerConfig.Port)
	assert.Equal(t, config.DefaultMode, merged.UpstreamConfig.Mode)
	require.Len(t, merged.RateLimit, 1)
	// the upstream list is the file's
	assert.Nil(t, upstreamById(t, merged, "eth-2"))
	require.NotNil(t, upstreamById(t, merged, "eth-3"))
	// chain-defaults from the file are baked into the upstreams, under the startup mode
	assert.Equal(t, time.Minute, upstreamById(t, merged, "eth-1").PollInterval)
	assert.Equal(t, time.Minute, upstreamById(t, merged, "eth-3").PollInterval)
	// the budget reference validates against the startup budgets
	assert.Equal(t, "standard-budget", upstreamById(t, merged, "eth-1").RateLimitBudget)
}

func TestLoadReloadableUnchangedFileYieldsStartupConfig(t *testing.T) {
	startup := readReloadFixture(t, "startup.yaml")
	t.Setenv(config.ConfigPathVar, "configs/reload/startup.yaml")
	loaded, err := config.NewAppConfig()
	require.NoError(t, err)

	merged, err := config.LoadReloadable(startup, startup)
	require.NoError(t, err)

	assert.Equal(t, loaded.UpstreamConfig.Upstreams, merged.UpstreamConfig.Upstreams)
}

func TestLoadReloadableRejectsUnknownBudget(t *testing.T) {
	_, err := config.LoadReloadable(readReloadFixture(t, "startup.yaml"), readReloadFixture(t, "missing-budget.yaml"))
	assert.ErrorContains(t, err, "non-existent rate limit budget 'unknown-budget'")
}

func TestLoadReloadableRejectsEmptyUpstreamList(t *testing.T) {
	_, err := config.LoadReloadable(readReloadFixture(t, "startup.yaml"), readReloadFixture(t, "no-upstreams.yaml"))
	assert.ErrorContains(t, err, "there must be at least one upstream in the config")
}

func TestLoadReloadableRejectsFileWithoutUpstreamConfig(t *testing.T) {
	_, err := config.LoadReloadable(readReloadFixture(t, "startup.yaml"), readReloadFixture(t, "no-upstream-config.yaml"))
	assert.ErrorContains(t, err, "there must be at least one upstream in the config")
}

func TestLoadReloadableRejectsBrokenYaml(t *testing.T) {
	_, err := config.LoadReloadable(readReloadFixture(t, "startup.yaml"), []byte("upstream-config: ["))
	assert.Error(t, err)
}

func TestRestartOnlyChangesReportsEverythingButReloadableFields(t *testing.T) {
	changed, err := config.RestartOnlyChanges(readReloadFixture(t, "startup.yaml"), readReloadFixture(t, "upstreams-changed.yaml"))
	require.NoError(t, err)

	// sorted; integrity is not in the list because both modes default it to disabled
	assert.Equal(t, []string{
		"chain-defaults.ethereum.dispatch",
		"rate-limit",
		"server",
		"upstream-config.mode",
	}, changed)
}

func TestRestartOnlyChangesIsEmptyForSameFile(t *testing.T) {
	startup := readReloadFixture(t, "startup.yaml")
	changed, err := config.RestartOnlyChanges(startup, startup)
	require.NoError(t, err)
	assert.Empty(t, changed)
}

func TestRestartOnlyChangesIgnoresUpstreamAndPollIntervalChanges(t *testing.T) {
	startup := readReloadFixture(t, "startup.yaml")
	file := []byte(`
server:
  port: 9095

rate-limit:
  - budgets:
      - name: standard-budget
        config:
          rules:
            - method: eth_getBlockByNumber
              requests: 100
              period: 1s

upstream-config:
  mode: default
  chain-defaults:
    ethereum:
      poll-interval: 10s
      dispatch:
        broadcast: true
  upstreams:
    - id: eth-9
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://eth-9.test
`)
	changed, err := config.RestartOnlyChanges(startup, file)
	require.NoError(t, err)
	assert.Empty(t, changed)
}
