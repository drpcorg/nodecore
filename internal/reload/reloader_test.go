package reload_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/reload"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	specs_utils.LoadMethodSpecs()
	os.Exit(m.Run())
}

const startupYaml = `
rate-limit:
  - budgets:
      - name: standard-budget
        config:
          rules:
            - method: eth_getBlockByNumber
              requests: 100
              period: 1s

upstream-config:
  upstreams:
    - id: eth-1
      chain: ethereum
      rate-limit-budget: standard-budget
      connectors:
        - type: json-rpc
          url: https://eth-1.test
`

const changedYaml = `
server:
  port: 9099

upstream-config:
  upstreams:
    - id: eth-1
      chain: ethereum
      rate-limit-budget: standard-budget
      connectors:
        - type: json-rpc
          url: https://eth-1.test
    - id: eth-2
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://eth-2.test
`

const brokenYaml = `
upstream-config:
  upstreams:
    - id: eth-1
      chain: ethereum
      rate-limit-budget: unknown-budget
      connectors:
        - type: json-rpc
          url: https://eth-1.test
`

type applierStub struct {
	mu    sync.Mutex
	calls [][]string
	block chan struct{} // when non-nil, ApplyUpstreams waits for it to close
}

func (a *applierStub) ApplyUpstreams(confs []*config.Upstream) upstreams.UpstreamsDiff {
	ids := make([]string, 0, len(confs))
	for _, conf := range confs {
		ids = append(ids, conf.Id)
	}
	a.mu.Lock()
	a.calls = append(a.calls, ids)
	block := a.block
	a.mu.Unlock()
	if block != nil {
		<-block
	}
	return upstreams.UpstreamsDiff{Added: ids}
}

func (a *applierStub) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *applierStub) lastCall() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.calls) == 0 {
		return nil
	}
	return a.calls[len(a.calls)-1]
}

type fixture struct {
	path     string
	startup  *config.AppConfig
	applier  *applierStub
	reloader *reload.Reloader
}

func newFixture(t *testing.T, watchInterval string) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodecore.yml")
	content := startupYaml
	if watchInterval != "" {
		content = "config-reload:\n  watch-interval: " + watchInterval + "\n" + startupYaml
	}
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv(config.ConfigPathVar, path)
	startup, err := config.NewAppConfig()
	require.NoError(t, err)

	applier := &applierStub{}
	return &fixture{
		path:     path,
		startup:  startup,
		applier:  applier,
		reloader: reload.NewReloader(path, startup, applier),
	}
}

func (f *fixture) write(t *testing.T, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.path, []byte(content), 0o600))
}

func TestReloadUnchangedFileDoesNotApply(t *testing.T) {
	f := newFixture(t, "")

	f.reloader.Reload("test")

	assert.Equal(t, 0, f.applier.callCount())
}

func TestReloadChangedFileAppliesItsUpstreams(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, changedYaml)

	f.reloader.Reload("test")

	assert.Equal(t, 1, f.applier.callCount())
	assert.Equal(t, []string{"eth-1", "eth-2"}, f.applier.lastCall())
}

func TestReloadSameContentTwiceAppliesOnce(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, changedYaml)

	f.reloader.Reload("test")
	f.reloader.Reload("test")

	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadRejectedFileIsNotAppliedAndNotRetried(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, brokenYaml)

	f.reloader.Reload("test")
	f.reloader.Reload("test")

	assert.Equal(t, 0, f.applier.callCount())

	// a fixed file is applied again
	f.write(t, changedYaml)
	f.reloader.Reload("test")
	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadMissingFileLeavesHandledHashUntouched(t *testing.T) {
	f := newFixture(t, "")
	require.NoError(t, os.Remove(f.path))

	f.reloader.Reload("test")
	assert.Equal(t, 0, f.applier.callCount())

	f.write(t, changedYaml)
	f.reloader.Reload("test")
	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadInProgressDropsTheSecondTrigger(t *testing.T) {
	f := newFixture(t, "")
	f.applier.block = make(chan struct{})
	f.write(t, changedYaml)

	finished := make(chan struct{})
	go func() {
		f.reloader.Reload("first")
		close(finished)
	}()
	require.Eventually(t, func() bool { return f.applier.callCount() == 1 }, time.Second, 10*time.Millisecond)

	returned := make(chan struct{})
	go func() {
		f.reloader.Reload("second")
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("a reload during a reload must return at once")
	}

	close(f.applier.block)
	<-finished
	assert.Equal(t, 1, f.applier.callCount())
}

func TestRunReloadsOnSignal(t *testing.T) {
	f := newFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hups := make(chan os.Signal, 1)
	go f.reloader.Run(ctx, hups)
	f.write(t, changedYaml)

	hups <- os.Interrupt // any signal value: Run does not inspect it

	assert.Eventually(t, func() bool { return f.applier.callCount() == 1 }, time.Second, 10*time.Millisecond)
}

func TestRunReloadsOnWatchTick(t *testing.T) {
	f := newFixture(t, "20ms")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.reloader.Run(ctx, make(chan os.Signal, 1))

	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, 0, f.applier.callCount())

	f.write(t, changedYaml)

	assert.Eventually(t, func() bool { return f.applier.callCount() == 1 }, time.Second, 10*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, 1, f.applier.callCount())
}

// yaml can leave pointers nil that the startup defaults never expected (a null mapping
// value); the startup path may panic on such a file, a reload must only refuse it.
func TestReloadRefusesFileThatPanicsTheDefaults(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, `
upstream-config:
  chain-defaults:
    ethereum:
  upstreams:
    - id: eth-1
      chain: ethereum
      connectors:
        - type: json-rpc
          url: https://eth-1.test
`)

	f.reloader.Reload("test")
	assert.Equal(t, 0, f.applier.callCount())

	// the content counts as handled: the same trigger again is a no-op, a fixed file applies
	f.reloader.Reload("test")
	f.write(t, changedYaml)
	f.reloader.Reload("test")
	assert.Equal(t, 1, f.applier.callCount())
}
