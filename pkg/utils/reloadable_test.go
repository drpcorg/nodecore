package utils_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	reloadPeriod = 20 * time.Millisecond
	waitFor      = 2 * time.Second
	tick         = 10 * time.Millisecond
)

var decodeCount atomic.Int32

// doc derives Upper from Name inside its unmarshal hooks, the way a real reloadable
// value builds its index.
type doc struct {
	Name  string `yaml:"name" json:"name"`
	Upper string `yaml:"-" json:"-"`
}

func (d *doc) init() error {
	decodeCount.Add(1)
	if d.Name == "bad" {
		return errors.New("bad doc")
	}
	d.Upper = strings.ToUpper(d.Name)
	return nil
}

func (d *doc) UnmarshalYAML(node *yaml.Node) error {
	type plain doc
	if err := node.Decode((*plain)(d)); err != nil {
		return err
	}
	return d.init()
}

func (d *doc) UnmarshalJSON(data []byte) error {
	type plain doc
	if err := sonic.Unmarshal(data, (*plain)(d)); err != nil {
		return err
	}
	return d.init()
}

func writeDoc(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestReloadableServesInitialBeforeFirstFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initial := &doc{Name: "init", Upper: "INIT"}

	holder := utils.NewReloadable(ctx, initial, filepath.Join(t.TempDir(), "missing.yaml"), utils.YAML, reloadPeriod)

	assert.Same(t, initial, holder.Get())
	time.Sleep(100 * time.Millisecond)
	assert.Same(t, initial, holder.Get())
}

func TestReloadableLoadsFromFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "doc.yaml")
	writeDoc(t, path, "name: a")

	holder := utils.NewReloadable(ctx, &doc{}, path, utils.YAML, reloadPeriod)

	assert.Eventually(t, func() bool { return holder.Get().Upper == "A" }, waitFor, tick)
	writeDoc(t, path, "name: b")
	assert.Eventually(t, func() bool { return holder.Get().Upper == "B" }, waitFor, tick)
}

func TestReloadableLoadsFromFileUrl(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "doc.yaml")
	writeDoc(t, path, "name: a")

	holder := utils.NewReloadable(ctx, &doc{}, "file://"+path, utils.YAML, reloadPeriod)

	assert.Eventually(t, func() bool { return holder.Get().Upper == "A" }, waitFor, tick)
}

func TestReloadableLoadsJsonFromHttp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"x"}`))
	}))
	defer server.Close()

	holder := utils.NewReloadable(ctx, &doc{}, server.URL, utils.JSON, reloadPeriod)

	assert.Eventually(t, func() bool { return holder.Get().Upper == "X" }, waitFor, tick)
}

func TestReloadableKeepsPreviousOnBadContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "doc.yaml")
	writeDoc(t, path, "name: a")
	holder := utils.NewReloadable(ctx, &doc{}, path, utils.YAML, reloadPeriod)
	require.Eventually(t, func() bool { return holder.Get().Upper == "A" }, waitFor, tick)

	writeDoc(t, path, "name: [")
	time.Sleep(3 * reloadPeriod)
	assert.Equal(t, "A", holder.Get().Upper)

	writeDoc(t, path, "name: bad")
	time.Sleep(3 * reloadPeriod)
	assert.Equal(t, "A", holder.Get().Upper)
}

func TestReloadableKeepsPreviousOnNon200(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"name":"x"}`))
	}))
	defer server.Close()
	initial := &doc{Name: "init", Upper: "INIT"}

	holder := utils.NewReloadable(ctx, initial, server.URL, utils.JSON, reloadPeriod)

	time.Sleep(3 * reloadPeriod)
	assert.Same(t, initial, holder.Get())
}

func TestReloadableSkipsUnchangedContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("name: same"))
	}))
	defer server.Close()
	decodeCount.Store(0)

	holder := utils.NewReloadable(ctx, &doc{}, server.URL, utils.YAML, reloadPeriod)

	require.Eventually(t, func() bool { return requests.Load() >= 3 }, waitFor, tick)
	assert.Equal(t, "SAME", holder.Get().Upper)
	assert.Equal(t, int32(1), decodeCount.Load())
}

func TestReloadableStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		<-release
		_, _ = w.Write([]byte("name: late"))
	}))
	defer server.Close()
	initial := &doc{Name: "init", Upper: "INIT"}

	holder := utils.NewReloadable(ctx, initial, server.URL, utils.YAML, reloadPeriod)

	require.Eventually(t, func() bool { return requests.Load() == 1 }, waitFor, tick)
	cancel()
	close(release)
	time.Sleep(3 * reloadPeriod)
	assert.Same(t, initial, holder.Get())
	assert.Equal(t, int32(1), requests.Load())
}
