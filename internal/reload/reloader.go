package reload

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/rs/zerolog/log"
)

// UpstreamsApplier is the part of the upstream supervisor a reload needs.
type UpstreamsApplier interface {
	ApplyUpstreams(confs []*config.Upstream) upstreams.UpstreamsDiff
}

// Reloader re-reads the config file while nodecore runs and applies its upstream list.
// Everything else in the file keeps its startup value; sections that differ are reported
// as needing a restart. The startup config is the baseline for the life of the process.
type Reloader struct {
	path    string
	startup *config.AppConfig
	applier UpstreamsApplier

	// inProgress makes a trigger that lands during a reload a no-op with a warning; nothing
	// is queued. last is only touched by the goroutine that owns the flag.
	inProgress atomic.Bool
	last       handled
}

func NewReloader(path string, startup *config.AppConfig, applier UpstreamsApplier) *Reloader {
	return &Reloader{
		path:    path,
		startup: startup,
		applier: applier,
		last:    handled{sum: sha256.Sum256(startup.Source())},
	}
}

// Run reloads on every signal from hups and, when a watch interval is configured, on every
// tick. Each reload runs in its own goroutine so the loop keeps draining triggers; a trigger
// during a reload is dropped by Reload itself. Returns when ctx is done.
func (r *Reloader) Run(ctx context.Context, hups <-chan os.Signal) {
	var ticks <-chan time.Time
	if interval := r.startup.ConfigReload.WatchInterval; interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
		log.Info().Msgf("the config file %s is checked every %s and reloaded on change, SIGHUP reloads it at once", r.path, interval)
	} else {
		log.Info().Msgf("the config file %s is reloaded on SIGHUP", r.path)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-hups:
			go r.Reload("signal " + sig.String())
		case <-ticks:
			go r.Reload("watch")
		}
	}
}

// Reload reads the file and, if its content differs from the last handled one, builds the
// config to apply, reports the restart-only differences, validates and applies the upstream
// list. trigger names what asked for the reload, for the logs.
func (r *Reloader) Reload(trigger string) {
	if !r.inProgress.CompareAndSwap(false, true) {
		log.Warn().Msgf("config reload is in progress, the %s trigger is ignored", trigger)
		return
	}
	defer r.inProgress.Store(false)

	file, err := os.ReadFile(r.path)
	if err != nil {
		// a file that is being replaced can be missing for a moment; the next trigger reads it
		log.Debug().Err(err).Msgf("couldn't read the config file %s, nothing is reloaded", r.path)
		return
	}
	sum := sha256.Sum256(file)
	if sum == r.last.sum {
		if r.last.err != nil {
			log.Warn().Err(r.last.err).Msg("config is unchanged since its last reload was rejected, change it to reload again")
		} else {
			log.Info().Msg("config is unchanged, nothing to reload")
		}
		return
	}

	log.Info().Msgf("reloading the config file %s (%s)", r.path, trigger)
	err = r.load(file)
	r.last = handled{sum: sum, err: err}
	if err != nil {
		log.Error().Err(err).Msg("config reload is rejected, the running upstreams are left untouched")
	}
}

// load reports the restart-only differences, builds and validates the config to apply and
// applies its upstream list. The config package was written for the startup path, where a
// file it cannot work with may take the process down; a reload must only refuse such a
// file, so a panic on the way is turned into the error that rejects it.
func (r *Reloader) load(file []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the config file can't be loaded: %v", recovered)
		}
	}()

	restartOnly, err := config.RestartOnlyChanges(r.startup.Source(), file)
	if err != nil {
		return err
	}
	for _, section := range restartOnly {
		log.Warn().Msgf("'%s' has changed in the config file but is only applied on restart", section)
	}
	merged, err := config.LoadReloadable(r.startup.Source(), file)
	if err != nil {
		return err
	}
	diff := r.applier.ApplyUpstreams(merged.UpstreamConfig.Upstreams)
	if diff.IsEmpty() {
		log.Info().Msg("config reloaded, no upstream changes")
	} else {
		log.Info().Msgf("upstreams reloaded: %s", diff)
	}
	return nil
}

// handled is the last file content a reload dealt with, applied or rejected, so that the
// same bytes are not processed twice. An operator who wants another attempt changes the file.
type handled struct {
	sum [sha256.Size]byte
	err error
}
