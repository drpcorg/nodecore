package flow

import (
	"sync"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

// LabelGroupStrategy balances requests across ordered priority groups of
// upstreams (grouped by config-defined group-labels). Groups are tried in
// order; within a group the usual rating order applies via filterUpstreams.
//
// Like the other strategies, a single instance lives for one request and its
// selectedUpstreams set accumulates across failsafe attempts (retries/hedges),
// so an upstream is selected at most once per request even if it belongs to
// several groups.
//
// passOnError controls retry routing after a retryable error (each error
// triggers a fresh SelectUpstream call):
//   - false (default): stay in the current group, picking the next untried
//     upstream; advance only once the current group has no selectable upstream.
//   - true: jump straight to the next group, skipping untried upstreams in the
//     current one.
//
// In both modes an entirely-dead group (nothing selectable) always falls
// through to the next group.
type LabelGroupStrategy struct {
	groups             [][]string
	passOnError        bool
	chainSupervisor    upstreams.ChainSupervisor
	selectedUpstreams  mapset.Set[string]
	additionalMatchers []Matcher
	order              UpstreamOrder
	currentGroupIdx    int
	firstCall          bool
	cursorMu           sync.Mutex
	mu                 sync.Mutex
}

// NewLabelGroupStrategy builds a LabelGroupStrategy from the rating registry:
// it takes the rating-sorted upstreams for (chain, method) and partitions them
// into ordered groups per the label-balancing config (see PartitionLabelGroups),
// reading each upstream's config group-labels via the supervisor. This mirrors
// NewRatingStrategy, which likewise pulls its sorted list from the registry.
func NewLabelGroupStrategy(
	chain chains.Chain,
	method string,
	labelBalancing *config.LabelBalancingConfig,
	chainSupervisor upstreams.ChainSupervisor,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	registry *rating.RatingRegistry,
) *LabelGroupStrategy {
	sorted := registry.GetSortedUpstreams(chain, method)
	includeDefault := labelBalancing.IncludeDefault == nil || *labelBalancing.IncludeDefault
	groups := PartitionLabelGroups(sorted, labelBalancing.Order, includeDefault, func(id string) mapset.Set[string] {
		up := upstreamSupervisor.GetUpstream(id)
		if up == nil {
			return mapset.NewThreadUnsafeSet[string]()
		}
		return up.GetGroupLabels()
	})
	return NewLabelGroupStrategyWithGroups(groups, labelBalancing.PassOnError, chainSupervisor)
}

// NewLabelGroupStrategyWithGroups builds a LabelGroupStrategy from already
// partitioned groups. NewLabelGroupStrategy is the usual entry point; this one
// is handy when the ordered groups are known up front (e.g. tests).
func NewLabelGroupStrategyWithGroups(groups [][]string, passOnError bool, chainSupervisor upstreams.ChainSupervisor) *LabelGroupStrategy {
	return &LabelGroupStrategy{
		groups:            groups,
		passOnError:       passOnError,
		chainSupervisor:   chainSupervisor,
		selectedUpstreams: mapset.NewThreadUnsafeSet[string](),
		firstCall:         true,
	}
}

func (s *LabelGroupStrategy) WithOrder(order UpstreamOrder) *LabelGroupStrategy {
	s.order = order
	return s
}

func (s *LabelGroupStrategy) WithAdditionalMatchers(additionalMatchers []Matcher) *LabelGroupStrategy {
	s.additionalMatchers = additionalMatchers
	return s
}

func (s *LabelGroupStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(s.groups) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	s.cursorMu.Lock()
	if s.passOnError && !s.firstCall {
		// a retryable error brought us back here: skip the rest of the current group
		s.currentGroupIdx++
	}
	s.firstCall = false
	idx := s.currentGroupIdx
	s.cursorMu.Unlock()

	var currentReason MatchResponse
	var trace *UpstreamsMatchTrace
	for ; idx < len(s.groups); idx++ {
		selectedUpstream, reason, groupTrace := filterUpstreams(&s.mu, request, s.groups[idx], s.chainSupervisor, s.selectedUpstreams, s.additionalMatchers, s.order)
		trace = groupTrace
		if selectedUpstream != "" {
			s.cursorMu.Lock()
			s.currentGroupIdx = idx
			s.cursorMu.Unlock()
			return selectedUpstream, nil
		}
		// group is exhausted (all tried) or dead (nothing selectable) -> fall through
		if reason != nil && (currentReason == nil || reason.Type() < currentReason.Type()) {
			currentReason = reason
		}
	}
	s.cursorMu.Lock()
	s.currentGroupIdx = idx
	s.cursorMu.Unlock()

	return "", selectionError(currentReason, trace)
}

var _ UpstreamStrategy = (*LabelGroupStrategy)(nil)

// PartitionLabelGroups builds the ordered group lists for a LabelGroupStrategy.
// sortedIds must be in rating order; that order is preserved within each group.
// For every label in order a group is produced containing the ids carrying that
// label (an id may appear in several groups). When includeDefault is true, ids
// carrying none of the order labels are appended as a final fallback group.
//
// labelsOf returns the set of group-labels for an upstream id; it is invoked
// once per id and the result reused, so membership checks are O(1).
func PartitionLabelGroups(sortedIds []string, order []string, includeDefault bool, labelsOf func(string) mapset.Set[string]) [][]string {
	labelSets := make(map[string]mapset.Set[string], len(sortedIds))
	for _, id := range sortedIds {
		labelSets[id] = labelsOf(id)
	}

	groups := make([][]string, 0, len(order)+1)
	for _, label := range order {
		group := make([]string, 0)
		for _, id := range sortedIds {
			if labelSets[id].ContainsOne(label) {
				group = append(group, id)
			}
		}
		groups = append(groups, group)
	}
	if includeDefault {
		defaultGroup := make([]string, 0)
		for _, id := range sortedIds {
			if !lo.SomeBy(order, func(label string) bool { return labelSets[id].ContainsOne(label) }) {
				defaultGroup = append(defaultGroup, id)
			}
		}
		if len(defaultGroup) > 0 {
			groups = append(groups, defaultGroup)
		}
	}
	return groups
}
