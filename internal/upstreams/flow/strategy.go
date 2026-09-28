package flow

import (
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const NoUpstream = "NoUpstream"

type UpstreamStrategy interface {
	SelectUpstream(request protocol.RequestHolder) (string, error)
}

type SpecificOrderUpstreamStrategy struct {
	selection
	upstreamIds        []string
	chainSupervisor    upstreams.ChainSupervisor
	additionalMatchers []Matcher
	order              UpstreamOrder
}

func (s *SpecificOrderUpstreamStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(s.upstreamIds) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	selectedUpstream, currentReason, trace := filterUpstreams(&s.selection, request, s.upstreamIds, s.chainSupervisor, s.additionalMatchers, s.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

func NewSpecificOrderUpstreamStrategy(upstreamIds []string, chainSupervisor upstreams.ChainSupervisor) *SpecificOrderUpstreamStrategy {
	return &SpecificOrderUpstreamStrategy{
		upstreamIds:     upstreamIds,
		chainSupervisor: chainSupervisor,
	}
}

var _ UpstreamStrategy = (*SpecificOrderUpstreamStrategy)(nil)

type RatingStrategy struct {
	selection
	chainSupervisor    upstreams.ChainSupervisor
	ups                []string
	additionalMatchers []Matcher
	order              UpstreamOrder
}

func NewRatingStrategy(
	chain chains.Chain,
	method string,
	additionalMatchers []Matcher,
	chainSupervisor upstreams.ChainSupervisor,
	registry *rating.RatingRegistry,
) *RatingStrategy {
	ups := registry.GetSortedUpstreams(chain, method)
	return &RatingStrategy{
		chainSupervisor:    chainSupervisor,
		ups:                ups,
		additionalMatchers: additionalMatchers,
	}
}

func (r *RatingStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(r.ups) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	selectedUpstream, currentReason, trace := filterUpstreams(&r.selection, request, r.ups, r.chainSupervisor, r.additionalMatchers, r.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

var _ UpstreamStrategy = (*RatingStrategy)(nil)

type GenericStrategy struct {
	selection
	chainSupervisor    upstreams.ChainSupervisor
	additionalMatchers []Matcher
	order              UpstreamOrder
}

func NewGenericStrategy(chainSupervisor upstreams.ChainSupervisor) *GenericStrategy {
	return &GenericStrategy{chainSupervisor: chainSupervisor}
}

func NewGenericStrategyWithOptions(chainSupervisor upstreams.ChainSupervisor, additionalMatchers []Matcher, order UpstreamOrder) *GenericStrategy {
	strategy := NewGenericStrategy(chainSupervisor)
	strategy.additionalMatchers = additionalMatchers
	strategy.order = order
	return strategy
}

func (r *RatingStrategy) WithOrder(order UpstreamOrder) *RatingStrategy {
	r.order = order
	return r
}

func (s *SpecificOrderUpstreamStrategy) WithOrder(order UpstreamOrder) *SpecificOrderUpstreamStrategy {
	s.order = order
	return s
}

func (s *SpecificOrderUpstreamStrategy) WithAdditionalMatchers(additionalMatchers []Matcher) *SpecificOrderUpstreamStrategy {
	s.additionalMatchers = additionalMatchers
	return s
}

func (b *GenericStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	upstreamIds := b.chainSupervisor.GetUpstreamIds()
	if len(upstreamIds) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	pos := b.chainSupervisor.NextIndex() % uint64(len(upstreamIds))
	upstreamIds = append(upstreamIds[pos:], upstreamIds[:pos]...)

	selectedUpstream, currentReason, trace := filterUpstreams(&b.selection, request, upstreamIds, b.chainSupervisor, b.additionalMatchers, b.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

func filterUpstreams(
	sel *selection,
	request protocol.RequestHolder,
	upstreamIds []string,
	chainSupervisor upstreams.ChainSupervisor,
	additionalMatchers []Matcher,
	order UpstreamOrder,
) (string, MatchResponse, *UpstreamsMatchTrace) {
	var currentReason MatchResponse
	trace := &UpstreamsMatchTrace{}
	if order != nil {
		upstreamIds = order(upstreamIds)
	}
	pins := sel.pinsOf(request)
	multiMatcher := requestMatcher(request, additionalMatchers)
	admitted := false
	for _, upstreamId := range upstreamIds {
		upstreamState := chainSupervisor.GetUpstreamState(upstreamId)
		// an upstream outside the pinned groups is no candidate, not even for the error
		if upstreamState == nil || !pins.admits(upstreamId, upstreamState) {
			continue
		}
		admitted = true
		matched := multiMatcher.Match(upstreamId, upstreamState)
		trace.Add(upstreamId, matched)

		upstreamMatched, newReason := sel.take(matched, currentReason, upstreamId, upstreamState, request)
		if upstreamMatched {
			allowed := true
			if upstreamState.AutoTuneRateLimiter != nil {
				allowed = upstreamState.AutoTuneRateLimiter.Allow()
			}
			if allowed {
				return upstreamId, nil, trace
			}
			if currentReason == nil || (RateLimiterResponse{}).Type() < currentReason.Type() {
				currentReason = RateLimiterResponse{}
			}
		} else if newReason != nil {
			currentReason = newReason
		}
	}
	if !admitted && pins.pinned() {
		return "", pins.notPresent(), trace
	}
	return "", currentReason, trace
}

// requestMatcher checks what an upstream must offer to serve the request.
func requestMatcher(request protocol.RequestHolder, additionalMatchers []Matcher) *MultiMatcher {
	// a fresh slice: the strategy's matchers are shared by concurrent hedges
	matchers := make([]Matcher, 0, len(additionalMatchers)+3)
	matchers = append(matchers, additionalMatchers...)
	matchers = append(matchers, NewStatusMatcher(), NewMethodMatcher(request.Method()))
	// a JSON-RPC subscription needs a live ws connector on the upstream; a gRPC
	// stream rides the grpc connector the spec already binds the method to
	if request.IsSubscribe() && request.RequestType() != protocol.Grpc {
		matchers = append(matchers, NewWsCapMatcher(request.Method()))
	}
	return NewMultiMatcher(matchers...)
}

// selection is what a strategy keeps for its one request across retries and
// hedges: the pins, parsed once, and the upstreams selected so far with the
// node group each was admitted under.
type selection struct {
	pinsOnce sync.Once
	pins     nodeGroupPins
	mu       sync.Mutex
	selected map[string]string // upstream id -> node group id
}

func (s *selection) pinsOf(request protocol.RequestHolder) nodeGroupPins {
	s.pinsOnce.Do(func() { s.pins = pinsOf(request) })
	return s.pins
}

// take selects the upstream if it matched, is not selected yet and has budget,
// naming its node group from the snapshot the gate admitted it on. Otherwise
// it returns the reason that replaces currentReason, if any.
func (s *selection) take(
	matched MatchResponse,
	currentReason MatchResponse,
	upstreamId string,
	state *protocol.UpstreamState,
	request protocol.RequestHolder,
) (bool, MatchResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.selected[upstreamId]; ok {
		return false, nil
	}
	if matched.Type() != SuccessType {
		if currentReason == nil || matched.Type() < currentReason.Type() {
			return false, matched
		}
		return false, nil
	}
	if state.RateLimiterBudget != nil {
		if allow, err := state.RateLimiterBudget.Allow(request.Method()); err != nil || !allow {
			return false, RateLimiterResponse{}
		}
	}
	if s.selected == nil {
		s.selected = make(map[string]string)
	}
	s.selected[upstreamId] = s.pinsOf(request).nodeGroupOf(upstreamId, state)
	return true, nil
}

func (s *selection) nodeGroupOf(upstreamId string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selected[upstreamId]
}

// nodeGroupRecorder is a strategy that names the node group it admitted each
// selected upstream under.
type nodeGroupRecorder interface {
	nodeGroupOf(upstreamId string) string
}

// selectedNodeGroup is the node group a reply of the selected upstream is
// stamped with: the one the gate admitted it under.
func selectedNodeGroup(strategy UpstreamStrategy, upstreamId string) string {
	if recorder, ok := strategy.(nodeGroupRecorder); ok {
		return recorder.nodeGroupOf(upstreamId)
	}
	return ""
}

func selectionError(matchResponse MatchResponse, trace *UpstreamsMatchTrace) error {
	if matchResponse == nil {
		return protocol.NoAvailableUpstreamsError()
	}
	switch m := matchResponse.(type) {
	case NodeGroupResponse:
		return protocol.NodeGroupNotPresentError(m.ids)
	case MethodResponse:
		return protocol.NotSupportedMethodError(m.method)
	case RateLimiterResponse:
		return protocol.RateLimitError()
	default:
		if matchResponse.Type() == SelectorType && trace != nil {
			return protocol.NoAvailableUpstreamsErrorWithCause(trace.Cause())
		}
		return protocol.NoAvailableUpstreamsError()
	}
}

var _ UpstreamStrategy = (*GenericStrategy)(nil)

// FailingStrategy is a sentinel strategy that returns the same preset error
// for every SelectUpstream call. Used by createStrategy to surface policy
// errors (e.g. quorum-not-supported) to the client without tying the check
// to a specific upstream selection path.
type FailingStrategy struct {
	err error
}

func NewFailingStrategy(err error) *FailingStrategy {
	return &FailingStrategy{err: err}
}

func (f *FailingStrategy) SelectUpstream(_ protocol.RequestHolder) (string, error) {
	return "", f.err
}

var _ UpstreamStrategy = (*FailingStrategy)(nil)
