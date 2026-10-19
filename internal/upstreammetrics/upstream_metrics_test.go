package upstreammetrics_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreammetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func series(vec prometheus.Collector) int {
	collected := make(chan prometheus.Metric, 100)
	vec.Collect(collected)
	close(collected)
	return len(collected)
}

func TestForgetDropsTheSeriesOfOneUpstream(t *testing.T) {
	// a name of its own per run: the default registry refuses a second one
	registeredName := fmt.Sprintf("forget_test_registered_%d", time.Now().UnixNano())
	withChain := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "forget_test_with_chain"}, []string{"chain", "method", "upstream"})
	withoutChain := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "forget_test_without_chain"}, []string{"upstream", "period"})
	registered := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: registeredName}, []string{"upstream"})
	upstreammetrics.Track(withChain, withoutChain)
	upstreammetrics.MustRegister(registered)

	for _, upstreamId := range []string{"gone", "stays"} {
		withChain.WithLabelValues("ethereum", "eth_call", upstreamId).Set(1)
		withChain.WithLabelValues("ethereum", "eth_getLogs", upstreamId).Set(1)
		withoutChain.WithLabelValues(upstreamId, "1s").Inc()
		registered.WithLabelValues(upstreamId).Observe(1)
	}

	upstreammetrics.Forget("gone")

	assert.Equal(t, 2, series(withChain))
	assert.Equal(t, 1, series(withoutChain))
	assert.Equal(t, 1, series(registered))
	// the registered one is exposed
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	exposed := false
	for _, family := range families {
		exposed = exposed || family.GetName() == registeredName
	}
	assert.True(t, exposed)
}

var (
	// a metric vector definition up to the closing brace of its label list
	vecDefinition = regexp.MustCompile(`(?s)(\w+)\s*=\s*prometheus\.New\w+Vec\(.*?\[\]string\{([^}]*)\}`)
	listedVecs    = regexp.MustCompile(`(?s)upstreammetrics\.(?:MustRegister|Track)\(([^)]*)\)`)
)

// Every metric vector in the code that has an "upstream" label must be in the
// list, or the series of a removed upstream stay behind. This test reads the
// sources, so a new metric can't be forgotten.
func TestEveryMetricWithAnUpstreamLabelIsListed(t *testing.T) {
	root := filepath.Join("..", "..")
	found := 0
	for _, dir := range []string{"internal", "pkg", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			listed := make(map[string]bool)
			for _, call := range listedVecs.FindAllStringSubmatch(string(source), -1) {
				for _, name := range strings.Split(call[1], ",") {
					listed[strings.TrimSpace(name)] = true
				}
			}
			for _, definition := range vecDefinition.FindAllStringSubmatch(string(source), -1) {
				name, labels := definition[1], definition[2]
				if !strings.Contains(labels, `"upstream"`) {
					continue
				}
				found++
				assert.Truef(t, listed[name], "%s: metric %s has an \"upstream\" label, register it with upstreammetrics.MustRegister", path, name)
			}
			return nil
		})
		require.NoError(t, err)
	}
	// the scan itself must keep working
	assert.GreaterOrEqual(t, found, 14)
}
