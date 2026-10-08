package upstreams

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// UpstreamsDiff is what separates the running upstream set from a wanted one,
// as sorted upstream ids.
type UpstreamsDiff struct {
	Added   []string
	Removed []string
	// Changed upstreams keep their id but not their config. They are replaced:
	// the running instance is removed and a new one is started.
	Changed []string
}

func (d UpstreamsDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0
}

func (d UpstreamsDiff) String() string {
	return fmt.Sprintf("added=%v, removed=%v, changed=%v", d.Added, d.Removed, d.Changed)
}

// diffUpstreams compares upstreams by id and by their whole config. The configs
// are expected to have their defaults applied, so a chain default that ends up
// in an upstream's settings counts as a change of that upstream.
func diffUpstreams(running map[string]*config.Upstream, wanted []*config.Upstream) UpstreamsDiff {
	diff := UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{}}

	wantedIds := mapset.NewThreadUnsafeSet[string]()
	for _, upConfig := range wanted {
		wantedIds.Add(upConfig.Id)
		runningConfig, ok := running[upConfig.Id]
		switch {
		case !ok:
			diff.Added = append(diff.Added, upConfig.Id)
		case !reflect.DeepEqual(runningConfig, upConfig):
			diff.Changed = append(diff.Changed, upConfig.Id)
		}
	}
	for id := range running {
		if !wantedIds.ContainsOne(id) {
			diff.Removed = append(diff.Removed, id)
		}
	}

	slices.Sort(diff.Added)
	slices.Sort(diff.Removed)
	slices.Sort(diff.Changed)
	return diff
}

// ApplyUpstreams makes the managed upstream set match the given list without
// touching anything else: upstreams that are new are started the way they are
// at startup, upstreams that are gone are removed, and upstreams whose config
// differs are replaced. Upstreams that did not change keep running untouched.
//
// The list is checked first and nothing is changed if it can't be applied as a
// whole. The call returns once the changes are under way; starting and stopping
// the upstreams themselves happens in the background.
func (b *GenericUpstreamSupervisor) ApplyUpstreams(upstreamConfigs []*config.Upstream) (UpstreamsDiff, error) {
	b.applyMu.Lock()
	defer b.applyMu.Unlock()

	if !b.started {
		return UpstreamsDiff{}, errors.New("upstreams are not started yet")
	}
	if err := b.validateUpstreams(upstreamConfigs); err != nil {
		return UpstreamsDiff{}, err
	}

	running := make(map[string]*config.Upstream, len(b.managed))
	for id, managed := range b.managed {
		running[id] = managed.config
	}
	diff := diffUpstreams(running, upstreamConfigs)
	if diff.IsEmpty() {
		return diff, nil
	}

	for _, id := range slices.Concat(diff.Removed, diff.Changed) {
		managed := b.managed[id]
		delete(b.managed, id)
		b.retired[id] = managed.done
		managed.cancel()
	}

	toStart := mapset.NewThreadUnsafeSet(slices.Concat(diff.Added, diff.Changed)...)
	for _, upConfig := range upstreamConfigs {
		if toStart.ContainsOne(upConfig.Id) {
			b.startUpstream(upConfig, true)
		}
	}

	return diff, nil
}

// validateUpstreams rejects a list that the supervisor could not run. It covers
// what would otherwise fail only when an upstream is being created, which on a
// running process is too late. applyMu must be held.
func (b *GenericUpstreamSupervisor) validateUpstreams(upstreamConfigs []*config.Upstream) error {
	if len(upstreamConfigs) == 0 {
		return errors.New("there must be at least one upstream")
	}

	ids := mapset.NewThreadUnsafeSet[string]()
	newIds := 0
	for _, upConfig := range upstreamConfigs {
		if upConfig == nil || upConfig.Id == "" {
			return errors.New("there is an upstream without id")
		}
		if !ids.Add(upConfig.Id) {
			return fmt.Errorf("upstream with id '%s' already exists", upConfig.Id)
		}
		if !chains.IsSupported(upConfig.ChainName) {
			return fmt.Errorf("upstream '%s' has not supported chain '%s'", upConfig.Id, upConfig.ChainName)
		}
		if upConfig.RateLimitBudget != "" {
			// budgets are created at startup and are not reloaded
			if b.rateLimitBudgetRegistry == nil {
				return fmt.Errorf("upstream '%s' references non-existent rate limit budget '%s'", upConfig.Id, upConfig.RateLimitBudget)
			}
			if _, ok := b.rateLimitBudgetRegistry.Get(upConfig.RateLimitBudget); !ok {
				return fmt.Errorf("upstream '%s' references rate limit budget '%s' that doesn't exist in the running process", upConfig.Id, upConfig.RateLimitBudget)
			}
		}
		if _, ok := b.upstreamIndices[upConfig.Id]; !ok {
			newIds++
		}
	}
	if b.upstreamIndicesCounter+newIds > maxUpstreamIndex {
		return fmt.Errorf("upstream indices overflow, max is %d", maxUpstreamIndex)
	}

	return nil
}
