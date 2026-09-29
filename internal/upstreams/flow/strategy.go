package flow

import (
	"slices"
	"sync"

	mapset "github.com/deckarep/golang-set/v2"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

const NoUpstream = "NoUpstream"

type UpstreamStrategy interface {
	SelectUpstream(request protocol.RequestHolder) (string, error)
}

type SpecificOrderUpstreamStrategy struct {
	upstreamIds        []string
	chainSupervisor    upstreams.ChainSupervisor
	selectedUpstreams  mapset.Set[string]
	additionalMatchers []Matcher
	order              UpstreamOrder
	mu                 sync.Mutex
}

func (s *SpecificOrderUpstreamStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if len(s.upstreamIds) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	selectedUpstream, currentReason, trace := filterUpstreams(&s.mu, request, s.upstreamIds, s.chainSupervisor, s.selectedUpstreams, s.additionalMatchers, s.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

func NewSpecificOrderUpstreamStrategy(upstreamIds []string, chainSupervisor upstreams.ChainSupervisor) *SpecificOrderUpstreamStrategy {
	return &SpecificOrderUpstreamStrategy{
		upstreamIds:       upstreamIds,
		chainSupervisor:   chainSupervisor,
		selectedUpstreams: mapset.NewThreadUnsafeSet[string](),
	}
}

var _ UpstreamStrategy = (*SpecificOrderUpstreamStrategy)(nil)

type RatingStrategy struct {
	chainSupervisor    upstreams.ChainSupervisor
	selectedUpstreams  mapset.Set[string]
	ups                []string
	additionalMatchers []Matcher
	order              UpstreamOrder
	mu                 sync.Mutex
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
		selectedUpstreams:  mapset.NewThreadUnsafeSet[string](),
	}
}

func (r *RatingStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	ups := withUnrated(request, r.ups, r.chainSupervisor)
	if len(ups) == 0 {
		return "", noUpstreamsError(request)
	}

	selectedUpstream, currentReason, trace := filterUpstreams(&r.mu, request, ups, r.chainSupervisor, r.selectedUpstreams, r.additionalMatchers, r.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

var _ UpstreamStrategy = (*RatingStrategy)(nil)

type GenericStrategy struct {
	selectedUpstreams  mapset.Set[string]
	chainSupervisor    upstreams.ChainSupervisor
	additionalMatchers []Matcher
	order              UpstreamOrder
	mu                 sync.Mutex
}

func NewGenericStrategy(chainSupervisor upstreams.ChainSupervisor) *GenericStrategy {
	return &GenericStrategy{
		selectedUpstreams: mapset.NewThreadUnsafeSet[string](),
		chainSupervisor:   chainSupervisor,
	}
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
		return "", noUpstreamsError(request)
	}

	pos := b.chainSupervisor.NextIndex() % uint64(len(upstreamIds))
	upstreamIds = append(upstreamIds[pos:], upstreamIds[:pos]...)

	selectedUpstream, currentReason, trace := filterUpstreams(&b.mu, request, upstreamIds, b.chainSupervisor, b.selectedUpstreams, b.additionalMatchers, b.order)
	if selectedUpstream != "" {
		return selectedUpstream, nil
	}

	return "", selectionError(currentReason, trace)
}

func filterUpstreams(
	mu *sync.Mutex,
	request protocol.RequestHolder,
	upstreamIds []string,
	chainSupervisor upstreams.ChainSupervisor,
	selectedUpstreams mapset.Set[string],
	additionalMatchers []Matcher,
	order UpstreamOrder,
) (string, MatchResponse, *UpstreamsMatchTrace) {
	var currentReason MatchResponse
	trace := &UpstreamsMatchTrace{}
	if order != nil {
		upstreamIds = order(upstreamIds)
	}
	pins := pinsOf(request)
	matchers := lo.Ternary(len(additionalMatchers) > 0, additionalMatchers, make([]Matcher, 0))
	matchers = append(matchers, NewStatusMatcher(), NewMethodMatcher(request.Method()))
	// a JSON-RPC subscription needs a live ws connector on the upstream; a gRPC
	// stream rides the grpc connector the spec already binds the method to
	if request.IsSubscribe() && request.RequestType() != protocol.Grpc {
		matchers = append(matchers, NewWsCapMatcher(request.Method()))
	}

	multiMatcher := NewMultiMatcher(matchers...)
	admitted := false
	for i := 0; i < len(upstreamIds); i++ {
		upstreamState := chainSupervisor.GetUpstreamState(upstreamIds[i])
		// an upstream outside the pins is no candidate, not even for the error
		if upstreamState == nil || !pins.admits(upstreamIds[i]) {
			continue
		}
		admitted = true
		matched := multiMatcher.Match(upstreamIds[i], upstreamState)
		trace.Add(upstreamIds[i], matched)

		upstreamMatched, newReason := processMatchedResponse(mu, matched, currentReason, selectedUpstreams, upstreamIds[i], upstreamState, request)
		if upstreamMatched {
			allowed := true
			if upstreamState.AutoTuneRateLimiter != nil {
				allowed = upstreamState.AutoTuneRateLimiter.Allow()
			}
			if allowed {
				return upstreamIds[i], nil, trace
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

// withUnrated appends to the rating list the pinned upstreams it lacks: the
// list is recomputed every calculation-interval, while the chain supervisor
// knows a new upstream at once.
func withUnrated(request protocol.RequestHolder, rated []string, chainSupervisor upstreams.ChainSupervisor) []string {
	pins := pinsOf(request)
	if !pins.pinned() || chainSupervisor == nil {
		return rated
	}
	var unrated []string
	for _, id := range pins[0] {
		if pins.admits(id) && !slices.Contains(rated, id) && !slices.Contains(unrated, id) && chainSupervisor.GetUpstreamState(id) != nil {
			unrated = append(unrated, id)
		}
	}
	if len(unrated) == 0 {
		return rated
	}
	return append(slices.Clip(rated), unrated...)
}

// noUpstreamsError: with no upstreams at all, none of the pinned ones is here.
func noUpstreamsError(request protocol.RequestHolder) error {
	if pins := pinsOf(request); pins.pinned() {
		return selectionError(pins.notPresent(), nil)
	}
	return protocol.NoAvailableUpstreamsError()
}

func processMatchedResponse(
	mu *sync.Mutex,
	matched MatchResponse,
	currentReason MatchResponse,
	selectedUpstreams mapset.Set[string],
	upstreamId string,
	state *protocol.UpstreamState,
	request protocol.RequestHolder,
) (bool, MatchResponse) {
	mu.Lock()
	defer mu.Unlock()
	if !selectedUpstreams.ContainsOne(upstreamId) {
		if matched.Type() == SuccessType {
			if state.RateLimiterBudget != nil {
				allow, err := state.RateLimiterBudget.Allow(request.Method())
				if err != nil {
					return false, RateLimiterResponse{}
				}
				if !allow {
					return false, RateLimiterResponse{}
				}
			}
			selectedUpstreams.Add(upstreamId)
			return true, nil
		} else {
			if currentReason == nil || matched.Type() < currentReason.Type() {
				return false, matched
			}
		}
	}
	return false, nil
}

func selectionError(matchResponse MatchResponse, trace *UpstreamsMatchTrace) error {
	if matchResponse == nil {
		return protocol.NoAvailableUpstreamsError()
	}
	switch m := matchResponse.(type) {
	case PinResponse:
		return protocol.PinnedUpstreamsNotPresentError(m.ids)
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
