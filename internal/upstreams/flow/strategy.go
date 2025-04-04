package flow

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	"sync/atomic"
)

const NoUpstream = "NoUpstream"

type UpstreamStrategy interface {
	SelectUpstream(request protocol.RequestHolder) (string, error)
}

var index = atomic.Uint64{}

type BaseStrategy struct {
	selectedUpstreams mapset.Set[string]
	chainSupervisor   *upstreams.ChainSupervisor
}

func NewBaseStrategy(chainSupervisor *upstreams.ChainSupervisor) *BaseStrategy {
	return &BaseStrategy{
		selectedUpstreams: mapset.NewSet[string](),
		chainSupervisor:   chainSupervisor,
	}
}

func (b *BaseStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	upstreamIds := b.chainSupervisor.GetUpstreamIds()
	if len(upstreamIds) == 0 {
		return "", protocol.NoAvailableUpstreamsError()
	}

	var currentReason MatchResponse = AvailabilityResponse{}

	pos := index.Add(1) % uint64(len(upstreamIds))
	upstreamIds = append(upstreamIds[pos:], upstreamIds[:pos]...)
	multiMatcher := NewMultiMatcher(NewStatusMatcher(), NewMethodMatcher(request.Method()))

	for i := 0; i < len(upstreamIds); i++ {
		upstreamState := b.chainSupervisor.GetUpstreamState(upstreamIds[i])
		matched := multiMatcher.Match(upstreamIds[i], upstreamState)

		if !b.selectedUpstreams.ContainsOne(upstreamIds[i]) {
			if matched.Type() == SuccessType {
				b.selectedUpstreams.Add(upstreamIds[i])
				return upstreamIds[i], nil
			} else {
				if matched.Type() < currentReason.Type() {
					currentReason = matched
				}
			}
		}
	}

	return "", selectionError(currentReason)
}

func selectionError(matchResponse MatchResponse) error {
	switch m := matchResponse.(type) {
	case MethodResponse:
		return protocol.NotSupportedMethodError(m.method)
	default:
		return protocol.NoAvailableUpstreamsError()
	}
}

var _ UpstreamStrategy = (*BaseStrategy)(nil)
