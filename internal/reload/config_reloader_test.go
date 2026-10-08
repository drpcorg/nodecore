package reload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ethOnly = `
upstream-config:
  upstreams:
    - id: eth-upstream
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://test.com
`

const ethAndPolygon = ethOnly + `
    - id: polygon-upstream
      chain: polygon
      connectors:
        - type: json-rpc
          url: https://test.com
`

func TestMain(m *testing.M) {
	specs_utils.LoadMethodSpecs()
	os.Exit(m.Run())
}

func metricValue(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, metric.Write(&m))
	if m.GetGauge() != nil {
		return m.GetGauge().GetValue()
	}
	return m.GetCounter().GetValue()
}

type applierStub struct {
	mu      sync.Mutex
	applied [][]string
	err     error
}

func (a *applierStub) ApplyUpstreams(upstreamConfigs []*config.Upstream) (upstreams.UpstreamsDiff, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return upstreams.UpstreamsDiff{}, a.err
	}
	ids := lo.Map(upstreamConfigs, func(item *config.Upstream, _ int) string { return item.Id })
	var previous []string
	if len(a.applied) > 0 {
		previous = a.applied[len(a.applied)-1]
	} else {
		previous = []string{"eth-upstream"}
	}
	a.applied = append(a.applied, ids)
	added, removed := lo.Difference(ids, previous)
	return upstreams.UpstreamsDiff{Added: added, Removed: removed}, nil
}

func (a *applierStub) calls() [][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]string{}, a.applied...)
}

func newTestReloader(t *testing.T, startup string) (*ConfigReloader, *applierStub, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodecore.yml")
	require.NoError(t, os.WriteFile(path, []byte(startup), 0o600))
	startupConfig, err := config.LoadAppConfig(path)
	require.NoError(t, err)

	applier := &applierStub{}
	return NewConfigReloader(path, startupConfig, applier), applier, path
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestReloadAppliesTheUpstreamList(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)
	writeConfig(t, path, ethAndPolygon)

	diff, err := reloader.Reload()

	require.NoError(t, err)
	assert.Equal(t, []string{"polygon-upstream"}, diff.Added)
	assert.Equal(t, [][]string{{"eth-upstream", "polygon-upstream"}}, applier.calls())
	assert.Equal(t, float64(1), metricValue(t, lastReloadSuccessfulMetric))
}

func TestReloadRejectsBrokenFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		err     string
	}{
		{name: "not yaml", content: "upstream-config: [", err: "yaml:"},
		{name: "empty file", content: "", err: "there must be at least one upstream in the config"},
		{name: "cut in the middle of an upstream", content: ethOnly + "    - id: polygon-upstream\n      chain: poly", err: "error during upstream 'polygon-upstream' validation, cause: not supported chain 'poly'"},
		{name: "unknown chain", content: ethOnly + "    - id: second\n      chain: no-such-chain\n      connectors:\n        - type: json-rpc\n          url: https://test.com\n", err: "not supported chain 'no-such-chain'"},
		{name: "duplicate id", content: ethOnly + "    - id: eth-upstream\n      chain: polygon\n      connectors:\n        - type: json-rpc\n          url: https://test.com\n", err: "upstream with id 'eth-upstream' already exists"},
		{name: "no upstreams", content: "upstream-config:\n  upstreams: []\n", err: "there must be at least one upstream in the config"},
		{name: "invalid server settings", content: "server:\n  port: -1\n" + ethOnly, err: "incorrect server port"},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			reloader, applier, path := newTestReloader(te, ethOnly)
			rejectedBefore := metricValue(t, reloadsMetric.WithLabelValues(resultRejected))
			writeConfig(te, path, test.content)

			diff, err := reloader.Reload()

			assert.ErrorContains(te, err, test.err)
			assert.True(te, diff.IsEmpty())
			assert.Empty(te, applier.calls())
			assert.Equal(te, float64(0), metricValue(te, lastReloadSuccessfulMetric))
			assert.Equal(te, rejectedBefore+1, metricValue(t, reloadsMetric.WithLabelValues(resultRejected)))
		})
	}
}

func TestReloadMissingFile(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)
	require.NoError(t, os.Remove(path))

	_, err := reloader.Reload()

	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, applier.calls())
}

func TestReloadKeepsTheErrorOfTheSupervisor(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)
	applier.err = errors.New("budget doesn't exist")
	writeConfig(t, path, ethAndPolygon)

	_, err := reloader.Reload()

	assert.EqualError(t, err, "budget doesn't exist")
	assert.Equal(t, float64(0), metricValue(t, lastReloadSuccessfulMetric))
}

func TestReloadRecoversAfterRejectedFile(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)

	writeConfig(t, path, "upstream-config: [")
	_, err := reloader.Reload()
	require.Error(t, err)

	writeConfig(t, path, ethAndPolygon)
	_, err = reloader.Reload()
	require.NoError(t, err)

	assert.Equal(t, [][]string{{"eth-upstream", "polygon-upstream"}}, applier.calls())
	assert.Equal(t, float64(1), metricValue(t, lastReloadSuccessfulMetric))
}

func TestWatchAppliesOnlyStableContent(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)

	// the startup content is looked at twice and then left alone
	reloader.watch()
	assert.Empty(t, applier.calls())
	reloader.watch()
	assert.Equal(t, [][]string{{"eth-upstream"}}, applier.calls())
	reloader.watch()
	reloader.watch()
	assert.Len(t, applier.calls(), 1)

	// a file that is still changing is not applied
	writeConfig(t, path, ethOnly+"    - id: polygon-upstream\n      chain: polygon\n      connectors:\n        - type: json-rpc\n          url: https://half-written.com\n")
	reloader.watch()
	writeConfig(t, path, ethAndPolygon)
	reloader.watch()
	assert.Len(t, applier.calls(), 1)

	// once it stays the same for two looks it is
	reloader.watch()
	assert.Equal(t, [][]string{{"eth-upstream"}, {"eth-upstream", "polygon-upstream"}}, applier.calls())
	reloader.watch()
	assert.Len(t, applier.calls(), 2)
}

func TestWatchReportsRejectedFileOnce(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)
	rejectedBefore := metricValue(t, reloadsMetric.WithLabelValues(resultRejected))
	writeConfig(t, path, "upstream-config: [")

	for i := 0; i < 5; i++ {
		reloader.watch()
	}

	assert.Empty(t, applier.calls())
	assert.Equal(t, rejectedBefore+1, metricValue(t, reloadsMetric.WithLabelValues(resultRejected)))

	// the fixed file is picked up
	writeConfig(t, path, ethAndPolygon)
	reloader.watch()
	reloader.watch()
	assert.Equal(t, [][]string{{"eth-upstream", "polygon-upstream"}}, applier.calls())
}

func TestWatchSurvivesMissingFile(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly)
	require.NoError(t, os.Remove(path))

	reloader.watch()
	reloader.watch()
	assert.Empty(t, applier.calls())

	writeConfig(t, path, ethAndPolygon)
	reloader.watch()
	reloader.watch()
	assert.Equal(t, [][]string{{"eth-upstream", "polygon-upstream"}}, applier.calls())
}

func TestRunWatchesTheFile(t *testing.T) {
	reloader, applier, path := newTestReloader(t, ethOnly+"  reload:\n    watch-interval: 100ms\n")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reloader.Run(ctx)
	}()

	writeConfig(t, path, ethAndPolygon+"  reload:\n    watch-interval: 100ms\n")

	require.Eventually(t, func() bool {
		calls := applier.calls()
		return len(calls) > 0 && len(calls[len(calls)-1]) == 2
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the reloader didn't stop with its context")
	}
}
