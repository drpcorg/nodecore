package reload_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/reload"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
	mu     sync.Mutex
	calls  [][]string
	block  chan struct{} // when non-nil, ApplyUpstreams waits for it to close
	failed []string      // ids reported as failed creations
}

func (a *applierStub) ApplyUpstreams(confs []*config.Upstream) upstreams.UpstreamsDiff {
	ids := make([]string, 0, len(confs))
	for _, conf := range confs {
		ids = append(ids, conf.Id)
	}
	a.mu.Lock()
	a.calls = append(a.calls, ids)
	block := a.block
	failed := a.failed
	a.mu.Unlock()
	if block != nil {
		<-block
	}
	return upstreams.UpstreamsDiff{Added: ids, Failed: failed}
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

	f.reloader.Reload(reload.SignalTrigger("test"))

	assert.Equal(t, 0, f.applier.callCount())
}

func TestReloadChangedFileAppliesItsUpstreams(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, changedYaml)

	f.reloader.Reload(reload.SignalTrigger("test"))

	assert.Equal(t, 1, f.applier.callCount())
	assert.Equal(t, []string{"eth-1", "eth-2"}, f.applier.lastCall())
}

func TestReloadSameContentTwiceAppliesOnce(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, changedYaml)

	f.reloader.Reload(reload.SignalTrigger("test"))
	f.reloader.Reload(reload.SignalTrigger("test"))

	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadRejectedFileIsNotAppliedAndNotRetried(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, brokenYaml)

	f.reloader.Reload(reload.SignalTrigger("test"))
	f.reloader.Reload(reload.SignalTrigger("test"))

	assert.Equal(t, 0, f.applier.callCount())

	// a fixed file is applied again
	f.write(t, changedYaml)
	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadMissingFileLeavesHandledHashUntouched(t *testing.T) {
	f := newFixture(t, "")
	require.NoError(t, os.Remove(f.path))

	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 0, f.applier.callCount())

	f.write(t, changedYaml)
	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 1, f.applier.callCount())
}

func TestReloadInProgressDropsTheSecondTrigger(t *testing.T) {
	f := newFixture(t, "")
	f.applier.block = make(chan struct{})
	f.write(t, changedYaml)

	finished := make(chan struct{})
	go func() {
		f.reloader.Reload(reload.SignalTrigger("first"))
		close(finished)
	}()
	require.Eventually(t, func() bool { return f.applier.callCount() == 1 }, time.Second, 10*time.Millisecond)

	returned := make(chan struct{})
	go func() {
		f.reloader.Reload(reload.SignalTrigger("second"))
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

	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 0, f.applier.callCount())

	// the content counts as handled: the same trigger again is a no-op, a fixed file applies
	f.reloader.Reload(reload.SignalTrigger("test"))
	f.write(t, changedYaml)
	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 1, f.applier.callCount())
}

// A creation failure can be environmental (a CA file not mounted yet), and the old instance
// of a changed id is kept, so retrying is free: the content is not recorded as handled and
// the next trigger applies it again.
func TestReloadRetriesContentWhoseUpstreamsFailedToCreate(t *testing.T) {
	f := newFixture(t, "")
	f.applier.failed = []string{"eth-2"}
	f.write(t, changedYaml)

	f.reloader.Reload(reload.SignalTrigger("test"))
	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 2, f.applier.callCount())

	// once the creation succeeds the content counts as handled
	f.applier.mu.Lock()
	f.applier.failed = nil
	f.applier.mu.Unlock()
	f.reloader.Reload(reload.SignalTrigger("test"))
	f.reloader.Reload(reload.SignalTrigger("test"))
	assert.Equal(t, 3, f.applier.callCount())
}

// captureLogs routes the global logger into a buffer for the test, at Debug level so every
// line is visible together with its level.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	t.Cleanup(func() { log.Logger = previous })
	return &buf
}

func logLines(buf *bytes.Buffer, text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, text) {
			lines = append(lines, line)
		}
	}
	return lines
}

// A watch tick asks the reloader a question it asks itself every interval, so an unchanged
// file is a Debug line; a signal is an operator asking, and gets the answer at Info.
func TestReloadUnchangedFileLogsAtDebugOnWatchAndInfoOnSignal(t *testing.T) {
	f := newFixture(t, "")
	logs := captureLogs(t)

	f.reloader.Reload(reload.WatchTrigger)
	watchLines := logLines(logs, "config is unchanged")
	require.Len(t, watchLines, 1)
	assert.Contains(t, watchLines[0], `"level":"debug"`)

	logs.Reset()
	f.reloader.Reload(reload.SignalTrigger("hangup"))
	signalLines := logLines(logs, "config is unchanged")
	require.Len(t, signalLines, 1)
	assert.Contains(t, signalLines[0], `"level":"info"`)
}

// The same for a file that was rejected: the error is repeated at Warn only when asked.
func TestReloadRejectedFileLogsAtDebugOnWatchAndWarnOnSignal(t *testing.T) {
	f := newFixture(t, "")
	f.write(t, brokenYaml)
	f.reloader.Reload(reload.SignalTrigger("test"))
	logs := captureLogs(t)

	f.reloader.Reload(reload.WatchTrigger)
	watchLines := logLines(logs, "unchanged since its last reload was rejected")
	require.Len(t, watchLines, 1)
	assert.Contains(t, watchLines[0], `"level":"debug"`)

	logs.Reset()
	f.reloader.Reload(reload.SignalTrigger("hangup"))
	signalLines := logLines(logs, "unchanged since its last reload was rejected")
	require.Len(t, signalLines, 1)
	assert.Contains(t, signalLines[0], `"level":"warn"`)
}

func TestReloadInProgressLogsAtDebugOnWatchAndWarnOnSignal(t *testing.T) {
	f := newFixture(t, "")
	f.applier.block = make(chan struct{})
	f.write(t, changedYaml)
	finished := make(chan struct{})
	go func() {
		f.reloader.Reload(reload.SignalTrigger("first"))
		close(finished)
	}()
	require.Eventually(t, func() bool { return f.applier.callCount() == 1 }, time.Second, 10*time.Millisecond)
	logs := captureLogs(t)

	f.reloader.Reload(reload.WatchTrigger)
	f.reloader.Reload(reload.SignalTrigger("hangup"))

	lines := logLines(logs, "config reload is in progress")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"level":"debug"`)
	assert.Contains(t, lines[1], `"level":"warn"`)

	close(f.applier.block)
	<-finished
}

// An incomplete reload still applied its successful parts, so the running set matches neither
// the previous content nor the new one. Reverting the file must therefore be applied, not
// skipped as "unchanged".
func TestReloadAppliesRevertAfterIncompleteReload(t *testing.T) {
	f := newFixture(t, "")
	f.applier.failed = []string{"eth-2"}
	f.write(t, changedYaml)
	f.reloader.Reload(reload.SignalTrigger("test"))
	require.Equal(t, 1, f.applier.callCount())

	f.applier.mu.Lock()
	f.applier.failed = nil
	f.applier.mu.Unlock()
	f.write(t, startupYaml) // the content the process started from

	f.reloader.Reload(reload.SignalTrigger("test"))

	assert.Equal(t, 2, f.applier.callCount())
	assert.Equal(t, []string{"eth-1"}, f.applier.lastCall())
}
