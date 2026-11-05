package flow

import (
	"slices"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
)

// A pinned attempt has one candidate. Shared selection still checks status,
// methods, selectors and rate limits, and records the candidate as used once.
type pinnedStrategy struct {
	*SpecificOrderUpstreamStrategy
	id string
}

func newPinnedStrategy(id string, supervisor upstreams.ChainSupervisor, matchers []Matcher) *pinnedStrategy {
	return &pinnedStrategy{SpecificOrderUpstreamStrategy: NewSpecificOrderUpstreamStrategy([]string{id}, supervisor).WithAdditionalMatchers(slices.Clip(matchers)), id: id}
}

func (s *pinnedStrategy) SelectUpstream(request protocol.RequestHolder) (string, error) {
	if s.chainSupervisor.GetUpstreamState(s.id) == nil {
		return "", protocol.UpstreamNotPresentError(s.id)
	}
	id, err := s.SpecificOrderUpstreamStrategy.SelectUpstream(request)
	if err != nil {
		// These are selection failures of this node, not invalid client requests.
		responseError := *err.(*protocol.ResponseError)
		responseError.NodeLevel = true
		return "", &responseError
	}
	return id, nil
}
