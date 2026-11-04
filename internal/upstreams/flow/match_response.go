package flow

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type MatchResponseType int

const (
	MethodType MatchResponseType = iota
	AvailabilityType
	RateLimiterType
	UpstreamIndexType
	SelectorType
	SuccessType
)

type MatchResponse interface {
	Cause() string
	Type() MatchResponseType
}

type RateLimiterResponse struct {
}

func (r RateLimiterResponse) Type() MatchResponseType {
	return RateLimiterType
}

func (r RateLimiterResponse) Cause() string {
	return "too many requests"
}

type UpstreamIndexResponse struct {
	upstreamIndex string
}

func (u UpstreamIndexResponse) Cause() string {
	return fmt.Sprintf("no upstream with index %s", u.upstreamIndex)
}

func (u UpstreamIndexResponse) Type() MatchResponseType {
	return UpstreamIndexType
}

var _ MatchResponse = (*UpstreamIndexResponse)(nil)

type SuccessResponse struct {
}

func (s SuccessResponse) Type() MatchResponseType {
	return SuccessType
}

func (s SuccessResponse) Cause() string {
	return ""
}

var _ MatchResponse = (*SuccessResponse)(nil)

type MethodResponse struct {
	method string
}

func (m MethodResponse) Type() MatchResponseType {
	return MethodType
}

func (m MethodResponse) Cause() string {
	return fmt.Sprintf("method %s is not supported", m.method)
}

var _ MatchResponse = (*MethodResponse)(nil)

type AvailabilityResponse struct {
}

func (a AvailabilityResponse) Type() MatchResponseType {
	return AvailabilityType
}

func (a AvailabilityResponse) Cause() string {
	return "upstream is not available"
}

var _ MatchResponse = (*AvailabilityResponse)(nil)

type LabelResponse struct {
	name   string
	values []string
}

func (l LabelResponse) Type() MatchResponseType { return SelectorType }
func (l LabelResponse) Cause() string {
	return fmt.Sprintf("No label `%s` with values %v", l.name, l.values)
}

type ExistsResponse struct{ name string }

func (e ExistsResponse) Type() MatchResponseType { return SelectorType }
func (e ExistsResponse) Cause() string           { return fmt.Sprintf("Label %s does not exist", e.name) }

type HeightResponse struct{ height, currentHeight int64 }

func (h HeightResponse) Type() MatchResponseType { return SelectorType }
func (h HeightResponse) Cause() string {
	return fmt.Sprintf("Upstream height %d is less than %d", h.currentHeight, h.height)
}

type SlotHeightResponse struct{ slot, currentSlotHeight int64 }

func (s SlotHeightResponse) Type() MatchResponseType { return SelectorType }
func (s SlotHeightResponse) Cause() string {
	return fmt.Sprintf("Upstream slot height %d is less than %d", s.currentSlotHeight, s.slot)
}

type LowerHeightResponse struct {
	lowerHeight, predictedHeight int64
	boundType                    protocol.LowerBoundType
}

func (l LowerHeightResponse) Type() MatchResponseType { return SelectorType }
func (l LowerHeightResponse) Cause() string {
	if l.predictedHeight == 0 {
		return fmt.Sprintf("Upstream lower height of type %s cannot be predicted", l.boundType.String())
	}
	return fmt.Sprintf("Upstream lower height %d of type %s is greater than %d", l.predictedHeight, l.boundType.String(), l.lowerHeight)
}

type VersionRangeResponse struct{ minVersion, maxVersion string }

func (v VersionRangeResponse) Type() MatchResponseType { return SelectorType }
func (v VersionRangeResponse) Cause() string {
	return fmt.Sprintf("version range from '%s' to '%s'", v.minVersion, v.maxVersion)
}

type SelectorUnsupportedResponse struct{ reason string }

func (s SelectorUnsupportedResponse) Type() MatchResponseType { return SelectorType }
func (s SelectorUnsupportedResponse) Cause() string           { return s.reason }

type NotMatchedResponse struct{ response MatchResponse }

func (n NotMatchedResponse) Type() MatchResponseType { return SelectorType }
func (n NotMatchedResponse) Cause() string           { return "Not matched - " + n.response.Cause() }

type MultiResponse struct{ responses []MatchResponse }

func (m MultiResponse) Type() MatchResponseType { return SelectorType }
func (m MultiResponse) Cause() string {
	parts := make([]string, 0)
	for _, r := range flattenResponses(m.responses) {
		if r.Type() != SuccessType {
			parts = append(parts, r.Cause())
		}
	}
	return joinCauses(parts)
}

type GroupLabelResponse struct{ labels []string }

func (g GroupLabelResponse) Type() MatchResponseType { return SelectorType }
func (g GroupLabelResponse) Cause() string {
	return fmt.Sprintf("Upstream has none of group-labels %v", g.labels)
}

func flattenResponses(responses []MatchResponse) []MatchResponse {
	out := make([]MatchResponse, 0, len(responses))
	for _, r := range responses {
		if m, ok := r.(MultiResponse); ok {
			out = append(out, flattenResponses(m.responses)...)
		} else {
			out = append(out, r)
		}
	}
	return out
}
func joinCauses(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "; " + p
	}
	return out
}
