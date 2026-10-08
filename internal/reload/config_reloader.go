package reload

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

const (
	resultApplied   = "applied"
	resultUnchanged = "unchanged"
	resultRejected  = "rejected"
)

var reloadsMetric = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: config.AppName,
		Subsystem: "config",
		Name:      "reloads_total",
		Help:      "The total number of config reload attempts by result: applied, unchanged or rejected",
	},
	[]string{"result"},
)

var lastReloadSuccessfulMetric = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "config",
		Name:      "last_reload_successful",
		Help:      "Whether the last config reload attempt succeeded: 1 = the running upstreams match the config file, 0 = the file was rejected",
	},
)

var lastReloadSuccessTimestampMetric = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Namespace: config.AppName,
		Subsystem: "config",
		Name:      "last_reload_success_timestamp_seconds",
		Help:      "Unix timestamp of the last successful config load or reload",
	},
)

func init() {
	prometheus.MustRegister(reloadsMetric, lastReloadSuccessfulMetric, lastReloadSuccessTimestampMetric)
}

// UpstreamsApplier is the part of the upstream supervisor a reload needs.
type UpstreamsApplier interface {
	ApplyUpstreams(upstreamConfigs []*config.Upstream) (upstreams.UpstreamsDiff, error)
}

// ConfigReloader re-reads the config file while nodecore is running and hands
// its upstream list over to the upstream supervisor. Nothing else in the file
// is applied: the rest of the config keeps its startup values.
//
// A file that can't be loaded as a whole is rejected and the running upstreams
// stay as they are.
type ConfigReloader struct {
	path          string
	startupConfig *config.AppConfig
	applier       UpstreamsApplier
	watchInterval time.Duration

	// mu serializes reloads, a SIGHUP may arrive in the middle of a watch tick
	mu sync.Mutex
	// handled is the content the last reload dealt with, applied or rejected,
	// so that the watch reports a file once and not on every tick
	handled   [sha256.Size]byte
	candidate [sha256.Size]byte
}

func NewConfigReloader(path string, startupConfig *config.AppConfig, applier UpstreamsApplier) *ConfigReloader {
	lastReloadSuccessfulMetric.Set(1)
	lastReloadSuccessTimestampMetric.SetToCurrentTime()

	return &ConfigReloader{
		path:          path,
		startupConfig: startupConfig,
		applier:       applier,
		watchInterval: startupConfig.UpstreamConfig.Reload.WatchInterval,
	}
}

// Run reloads the config on every SIGHUP and, if the watch is enabled, whenever
// the content of the config file changes. It returns when ctx is done.
func (r *ConfigReloader) Run(ctx context.Context) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	var ticks <-chan time.Time
	if r.watchInterval > 0 {
		ticker := time.NewTicker(r.watchInterval)
		defer ticker.Stop()
		ticks = ticker.C
		log.Info().Msgf("the config file %s is watched every %s, its upstream list is reloaded on change", r.path, r.watchInterval)
	} else {
		log.Info().Msgf("the config file %s is not watched, send SIGHUP to reload its upstream list", r.path)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			log.Info().Msg("got signal SIGHUP, reloading the config")
			if _, err := r.Reload(); err != nil {
				log.Error().Err(err).Msgf("config reload from %s is rejected, the running upstreams are left untouched", r.path)
			}
		case <-ticks:
			r.watch()
		}
	}
}

// Reload reads the config file and applies its upstream list right away.
func (r *ConfigReloader) Reload() (upstreams.UpstreamsDiff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := os.ReadFile(r.path)
	if err != nil {
		reloadFailed()
		return upstreams.UpstreamsDiff{}, err
	}
	diff, err := r.apply(file)
	if err == nil && diff.IsEmpty() {
		log.Info().Msg("config reload found no upstream changes")
	}
	return diff, err
}

// watch looks at the config file once. A changed file is applied only when two
// consecutive looks see the same content: a file caught in the middle of being
// written can still be valid YAML, and half of an upstream list must not be
// taken for the whole.
func (r *ConfigReloader) watch() {
	r.mu.Lock()
	defer r.mu.Unlock()

	file, err := os.ReadFile(r.path)
	if err != nil {
		// a file that is being replaced can be missing for a moment
		log.Debug().Err(err).Msgf("couldn't read the config file %s", r.path)
		r.candidate = [sha256.Size]byte{}
		return
	}
	sum := sha256.Sum256(file)
	if sum == r.handled {
		return
	}
	if sum != r.candidate {
		r.candidate = sum
		return
	}

	if _, err = r.apply(file); err != nil {
		log.Error().Err(err).Msgf("config reload from %s is rejected, the running upstreams are left untouched", r.path)
	}
}

func (r *ConfigReloader) apply(file []byte) (upstreams.UpstreamsDiff, error) {
	r.handled = sha256.Sum256(file)

	loaded, err := parseConfig(file)
	if err != nil {
		reloadFailed()
		return upstreams.UpstreamsDiff{}, err
	}
	if restartOnly := r.startupConfig.RestartOnlyChanges(loaded); len(restartOnly) > 0 {
		log.Warn().Msgf("config sections %v differ from the running config but only the upstream list is reloaded, restart nodecore to apply them", restartOnly)
	}

	diff, err := r.applier.ApplyUpstreams(loaded.UpstreamConfig.Upstreams)
	if err != nil {
		reloadFailed()
		return upstreams.UpstreamsDiff{}, err
	}

	lastReloadSuccessfulMetric.Set(1)
	lastReloadSuccessTimestampMetric.SetToCurrentTime()
	if diff.IsEmpty() {
		reloadsMetric.WithLabelValues(resultUnchanged).Inc()
		return diff, nil
	}
	reloadsMetric.WithLabelValues(resultApplied).Inc()
	log.Info().Msgf("upstreams have been reloaded: %s", diff)
	return diff, nil
}

func reloadFailed() {
	reloadsMetric.WithLabelValues(resultRejected).Inc()
	lastReloadSuccessfulMetric.Set(0)
}

// parseConfig never lets a broken file take the process down: the startup path
// may panic on a config it can't work with, a reload must only refuse it.
func parseConfig(file []byte) (loaded *config.AppConfig, err error) {
	defer func() {
		if r := recover(); r != nil {
			loaded, err = nil, fmt.Errorf("panic during the config parsing: %v", r)
		}
	}()
	return config.ParseAppConfig(file)
}
