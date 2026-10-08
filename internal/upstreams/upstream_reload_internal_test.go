package upstreams

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/ratelimiter"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
		{name: "no upstreams", upstream: nil, err: "there must be at least one upstream in the config"},
		{name: "nil upstream", upstream: []*config.Upstream{eth, nil}, err: "error during upstream validation, cause: no upstream id under index 1"},
		{name: "no id", upstream: []*config.Upstream{reloadUpstreamConfig("", "ethereum", "http://eth")}, err: "error during upstream validation, cause: no upstream id under index 0"},
		{
			name:     "duplicate id",
			upstream: []*config.Upstream{eth, reloadUpstreamConfig("eth", "polygon", "http://polygon")},
			err:      "error during upstream validation, cause: upstream with id 'eth' already exists",
		},
		{
			name:     "unknown chain",
			upstream: []*config.Upstream{eth, reloadUpstreamConfig("no", "no-such-chain", "http://no")},
			err:      "error during upstream 'no' validation, cause: not supported chain 'no-such-chain'",
		},
		{
			name: "unknown rate limit budget",
			upstream: []*config.Upstream{func() *config.Upstream {
				withBudget := reloadUpstreamConfig("eth", "ethereum", "http://eth")
				withBudget.RateLimitBudget = "budget"
				return withBudget
			}()},
			err: "upstream 'eth' references non-existent rate limit budget 'budget', budgets are created at startup",
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

// startPanickingUpstream starts for real and then panics, which is the worst
// case for a failed start: everything is already running.
type startPanickingUpstream struct {
	Upstream
	stopped atomic.Bool
}

func (p *startPanickingUpstream) Start() {
	p.Upstream.Start()
	panic("start failed")
}

func (p *startPanickingUpstream) Stop() {
	p.stopped.Store(true)
	p.Upstream.Stop()
}

// An upstream added by a reload whose start panics must be taken down again,
// not just forgotten: its connectors and loops are already running.
func TestApplyUpstreamsStopsUpstreamWhoseStartPanicked(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)
	supervisor.statsService = noStatsService{}
	supervisor.tracker = dimensions.NewGenericDimensionTracker()
	created := make(chan *startPanickingUpstream, 1)
	supervisor.createUpstream = func(
		ctx context.Context,
		conf *config.Upstream,
		tracker dimensions.DimensionTracker,
		statsService UpstreamStatsService,
		executor failsafe.Executor[protocol.ResponseHolder],
		upstreamIndex int,
		rateLimitBudgetRegistry *ratelimiter.RateLimitBudgetRegistry,
		torProxyUrl string,
	) (Upstream, error) {
		up, err := CreateUpstream(ctx, conf, tracker, statsService, executor, upstreamIndex, rateLimitBudgetRegistry, torProxyUrl)
		if err != nil {
			return nil, err
		}
		panicking := &startPanickingUpstream{Upstream: up}
		created <- panicking
		return panicking, nil
	}
	supervisor.StartUpstreams()

	appConfig, err := config.ParseAppConfig([]byte(`
upstream-config:
  upstreams:
    - id: eth
      chain: ethereum
      options:
        disable-validation: true
      connectors:
        - type: json-rpc
          url: http://127.0.0.1:1
`))
	require.NoError(t, err)
	_, err = supervisor.ApplyUpstreams(appConfig.UpstreamConfig.Upstreams)
	require.NoError(t, err)

	var up *startPanickingUpstream
	select {
	case up = <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream was not created")
	}
	supervisor.applyMu.Lock()
	done := supervisor.managed["eth"].done
	supervisor.applyMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine of the failed upstream is still there")
	}

	assert.True(t, up.stopped.Load(), "the upstream whose start panicked was not stopped")
	assert.False(t, up.Running())
	assert.Nil(t, supervisor.GetUpstream("eth"))
}

type noStatsService struct{}

func (noStatsService) AddRequestResults([]protocol.RequestResult) {}

func requestRegistryGoroutines() int {
	stacks := make([]byte, 4<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	return strings.Count(string(stacks), "ws.(*GenericRequestRegistry).run")
}

// A panic in the middle of CreateUpstream must release what was already bound
// to the upstream's context - here the request registry of its websocket.
func TestCreateUpstreamPanicReleasesItsContext(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	appConfig, err := config.ParseAppConfig([]byte(`
rate-limit:
  - budgets:
      - name: budget
        config:
          rules:
            - method: eth_call
              requests: 1
              period: 1s
upstream-config:
  upstreams:
    - id: eth
      chain: ethereum
      rate-limit-budget: budget
      connectors:
        - type: json-rpc
          url: http://127.0.0.1:1
        - type: websocket
          url: ws://127.0.0.1:1
`))
	require.NoError(t, err)
	before := requestRegistryGoroutines()

	// the budget is in the config but not in the registry, as after a reload
	// that brought a new budget: the creation panics on it after the
	// connectors are built
	registry, err := ratelimiter.NewRateLimitBudgetRegistry(nil, nil)
	require.NoError(t, err)
	assert.Panics(t, func() {
		_, _ = CreateUpstream(
			context.Background(),
			appConfig.UpstreamConfig.Upstreams[0],
			dimensions.NewGenericDimensionTracker(),
			noStatsService{},
			createUpstreamExecutor(appConfig.UpstreamConfig.Upstreams[0].FailsafeConfig),
			1,
			registry,
			"",
		)
	})

	assert.Eventually(t, func() bool { return requestRegistryGoroutines() <= before }, 5*time.Second, 10*time.Millisecond,
		"the request registry of the upstream that was never created is still running")
}

// The supervisor repeats the checks of the config validation on purpose, and
// must then say the same thing in the same words.
func TestValidateUpstreamsSpeaksLikeTheConfigValidation(t *testing.T) {
	supervisor := newReloadTestSupervisor(t)
	const header = "upstream-config:\n  upstreams:\n"
	const eth = "    - id: eth\n      chain: ethereum\n      connectors:\n        - type: json-rpc\n          url: http://eth\n"

	for name, file := range map[string]string{
		"no upstreams":  "upstream-config:\n  upstreams: []\n",
		"no id":         header + eth + "    - chain: polygon\n      connectors:\n        - type: json-rpc\n          url: http://polygon\n",
		"duplicate id":  header + eth + eth,
		"unknown chain": header + eth + "    - id: no\n      chain: no-such-chain\n      connectors:\n        - type: json-rpc\n          url: http://no\n",
	} {
		t.Run(name, func(te *testing.T) {
			_, configErr := config.ParseAppConfig([]byte(file))
			require.Error(te, configErr)

			// the same list, as the supervisor would get it from another caller
			var raw struct {
				UpstreamConfig struct {
					Upstreams []*config.Upstream `yaml:"upstreams"`
				} `yaml:"upstream-config"`
			}
			require.NoError(te, yaml.Unmarshal([]byte(file), &raw))
			supervisor.applyMu.Lock()
			supervisorErr := supervisor.validateUpstreams(raw.UpstreamConfig.Upstreams)
			supervisor.applyMu.Unlock()

			assert.EqualError(te, supervisorErr, configErr.Error())
		})
	}
}
