package upstreams

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reloadUpstreamConfig(id, chain, url string) *config.Upstream {
	return &config.Upstream{
		Id:           id,
		ChainName:    chain,
		PollInterval: time.Second,
		Connectors:   []*config.ApiConnectorConfig{{Type: "json-rpc", Url: url}},
		Options:      &chains.Options{InternalTimeout: 5 * time.Second, DisableValidation: new(false)},
	}
}

func TestDiffUpstreams(t *testing.T) {
	eth := reloadUpstreamConfig("eth", "ethereum", "http://eth")
	polygon := reloadUpstreamConfig("polygon", "polygon", "http://polygon")
	base := reloadUpstreamConfig("base", "base", "http://base")
	running := map[string]*config.Upstream{"eth": eth, "polygon": polygon, "base": base}

	tests := []struct {
		name     string
		wanted   []*config.Upstream
		expected UpstreamsDiff
	}{
		{
			name:     "the same list",
			wanted:   []*config.Upstream{eth, polygon, base},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{}},
		},
		{
			name: "an equal config under another pointer is not a change",
			wanted: []*config.Upstream{
				reloadUpstreamConfig("eth", "ethereum", "http://eth"),
				reloadUpstreamConfig("polygon", "polygon", "http://polygon"),
				reloadUpstreamConfig("base", "base", "http://base"),
			},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{}},
		},
		{
			name:     "the order doesn't matter",
			wanted:   []*config.Upstream{base, eth, polygon},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{}},
		},
		{
			name:     "added",
			wanted:   []*config.Upstream{eth, polygon, base, reloadUpstreamConfig("zk", "zksync", "http://zk"), reloadUpstreamConfig("arb", "arbitrum", "http://arb")},
			expected: UpstreamsDiff{Added: []string{"arb", "zk"}, Removed: []string{}, Changed: []string{}},
		},
		{
			name:     "removed",
			wanted:   []*config.Upstream{eth},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{"base", "polygon"}, Changed: []string{}},
		},
		{
			name:     "changed connector",
			wanted:   []*config.Upstream{eth, reloadUpstreamConfig("polygon", "polygon", "http://another-polygon"), base},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{"polygon"}},
		},
		{
			name: "changed option behind a pointer",
			wanted: []*config.Upstream{eth, polygon, func() *config.Upstream {
				changed := reloadUpstreamConfig("base", "base", "http://base")
				changed.Options.DisableValidation = new(true)
				return changed
			}()},
			expected: UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{"base"}},
		},
		{
			name:     "everything at once",
			wanted:   []*config.Upstream{reloadUpstreamConfig("eth", "ethereum", "http://eth-2"), base, reloadUpstreamConfig("arb", "arbitrum", "http://arb")},
			expected: UpstreamsDiff{Added: []string{"arb"}, Removed: []string{"polygon"}, Changed: []string{"eth"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.expected, diffUpstreams(running, test.wanted))
		})
	}
}

func TestUpstreamsDiffIsEmpty(t *testing.T) {
	assert.True(t, UpstreamsDiff{}.IsEmpty())
	assert.True(t, diffUpstreams(nil, nil).IsEmpty())
	assert.False(t, UpstreamsDiff{Added: []string{"id"}}.IsEmpty())
	assert.False(t, UpstreamsDiff{Removed: []string{"id"}}.IsEmpty())
	assert.False(t, UpstreamsDiff{Changed: []string{"id"}}.IsEmpty())
}

func newReloadTestSupervisor(t *testing.T) *GenericUpstreamSupervisor {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	supervisor := NewGenericUpstreamSupervisor(
		ctx,
		&config.UpstreamConfig{FailsafeConfig: &config.FailsafeConfig{}},
		nil,
		nil,
		nil,
		"",
	)
	return supervisor.(*GenericUpstreamSupervisor)
}

func TestValidateUpstreams(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)
	eth := reloadUpstreamConfig("eth", "ethereum", "http://eth")

	tests := []struct {
		name     string
		upstream []*config.Upstream
		err      string
	}{
		{name: "no upstreams", upstream: nil, err: "there must be at least one upstream"},
		{name: "nil upstream", upstream: []*config.Upstream{eth, nil}, err: "there is an upstream without id"},
		{name: "no id", upstream: []*config.Upstream{reloadUpstreamConfig("", "ethereum", "http://eth")}, err: "there is an upstream without id"},
		{
			name:     "duplicate id",
			upstream: []*config.Upstream{eth, reloadUpstreamConfig("eth", "polygon", "http://polygon")},
			err:      "upstream with id 'eth' already exists",
		},
		{
			name:     "unknown chain",
			upstream: []*config.Upstream{eth, reloadUpstreamConfig("no", "no-such-chain", "http://no")},
			err:      "upstream 'no' has not supported chain 'no-such-chain'",
		},
		{
			name: "unknown rate limit budget",
			upstream: []*config.Upstream{func() *config.Upstream {
				withBudget := reloadUpstreamConfig("eth", "ethereum", "http://eth")
				withBudget.RateLimitBudget = "budget"
				return withBudget
			}()},
			err: "upstream 'eth' references non-existent rate limit budget 'budget'",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			supervisor.applyMu.Lock()
			defer supervisor.applyMu.Unlock()

			assert.EqualError(te, supervisor.validateUpstreams(test.upstream), test.err)
		})
	}

	supervisor.applyMu.Lock()
	defer supervisor.applyMu.Unlock()
	assert.NoError(t, supervisor.validateUpstreams([]*config.Upstream{eth}))
}

func TestValidateUpstreamsIndicesOverflow(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)
	supervisor.applyMu.Lock()
	defer supervisor.applyMu.Unlock()

	supervisor.upstreamIndicesCounter = maxUpstreamIndex - 1
	one := []*config.Upstream{reloadUpstreamConfig("eth", "ethereum", "http://eth")}
	two := append(one, reloadUpstreamConfig("polygon", "polygon", "http://polygon"))

	assert.NoError(t, supervisor.validateUpstreams(one))
	assert.EqualError(t, supervisor.validateUpstreams(two), "upstream indices overflow, max is 1048575")
}

func TestUpstreamIndexIsBoundToId(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)
	supervisor.applyMu.Lock()
	defer supervisor.applyMu.Unlock()

	first, ok := supervisor.upstreamIndex("eth")
	require.True(t, ok)
	second, ok := supervisor.upstreamIndex("polygon")
	require.True(t, ok)
	again, ok := supervisor.upstreamIndex("eth")
	require.True(t, ok)

	assert.Equal(t, 1, first)
	assert.Equal(t, 2, second)
	assert.Equal(t, first, again)

	supervisor.upstreamIndicesCounter = maxUpstreamIndex
	_, ok = supervisor.upstreamIndex("base")
	assert.False(t, ok)
	// an id that already has an index keeps it even when there are no free ones
	again, ok = supervisor.upstreamIndex("eth")
	assert.True(t, ok)
	assert.Equal(t, first, again)
}

func TestApplyUpstreamsBeforeStart(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)

	_, err := supervisor.ApplyUpstreams([]*config.Upstream{reloadUpstreamConfig("eth", "ethereum", "http://eth")})

	assert.EqualError(t, err, "upstreams are not started yet")
}
