package flow

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/rs/zerolog/log"
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

type Matcher interface {
	Match(string, *protocol.UpstreamState) MatchResponse
}

type StatusMatcher struct{}

func NewStatusMatcher() *StatusMatcher {
	return &StatusMatcher{}
}

func (s *StatusMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state.Status == protocol.Available {
		return SuccessResponse{}
	}
	return AvailabilityResponse{}
}

var _ Matcher = (*StatusMatcher)(nil)

type MethodMatcher struct {
	method string
}

func (m *MethodMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state.UpstreamMethods.HasMethod(m.method) {
		return SuccessResponse{}
	}
	return MethodResponse{method: m.method}
}

func NewMethodMatcher(method string) *MethodMatcher {
	return &MethodMatcher{method: method}
}

var _ Matcher = (*MethodMatcher)(nil)

type WsCapMatcher struct {
	method string
}

func (w *WsCapMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state.Caps.ContainsOne(protocol.WsCap) {
		return SuccessResponse{}
	}
	return MethodResponse{method: w.method}
}

var _ Matcher = (*WsCapMatcher)(nil)

func NewWsCapMatcher(method string) *WsCapMatcher {
	return &WsCapMatcher{method: method}
}

type UpstreamIndexMatcher struct {
	upstreamIndex string
}

func (u *UpstreamIndexMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state.UpstreamIndex == u.upstreamIndex {
		return SuccessResponse{}
	}
	return UpstreamIndexResponse{upstreamIndex: u.upstreamIndex}
}

var _ Matcher = (*UpstreamIndexMatcher)(nil)

func NewUpstreamIndexMatcher(upstreamIndex string) *UpstreamIndexMatcher {
	return &UpstreamIndexMatcher{upstreamIndex: upstreamIndex}
}

type MultiMatcher struct {
	matchers []Matcher
}

func (m *MultiMatcher) Match(upId string, state *protocol.UpstreamState) MatchResponse {
	var response MatchResponse = SuccessResponse{}
	for _, matcher := range m.matchers {
		matchedResponse := matcher.Match(upId, state)
		if matchedResponse.Type() != SuccessType {
			log.Debug().Msgf("upstream %s check: %s", upId, matchedResponse.Cause())
		}
		if matchedResponse.Type() < response.Type() {
			response = matchedResponse
		}
	}
	return response
}

func NewMultiMatcher(matchers ...Matcher) *MultiMatcher {
	return &MultiMatcher{matchers: matchers}
}

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

type LabelMatcher struct {
	name   string
	values []string
}

func NewLabelMatcher(name string, values []string) *LabelMatcher {
	return &LabelMatcher{name: name, values: values}
}
func (l *LabelMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state == nil || state.Labels == nil {
		return LabelResponse{l.name, l.values}
	}
	value, ok := state.Labels.GetLabel(l.name)
	if !ok {
		return LabelResponse{l.name, l.values}
	}
	if len(l.values) == 0 {
		return SuccessResponse{}
	}
	for _, expected := range l.values {
		if value == expected {
			return SuccessResponse{}
		}
	}
	return LabelResponse{l.name, l.values}
}

type LabelExistsMatcher struct{ name string }

func NewLabelExistsMatcher(name string) *LabelExistsMatcher { return &LabelExistsMatcher{name: name} }
func (l *LabelExistsMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state != nil && state.Labels != nil {
		if _, ok := state.Labels.GetLabel(l.name); ok {
			return SuccessResponse{}
		}
	}
	return ExistsResponse{l.name}
}

type HeightMatcher struct{ height int64 }

func NewHeightMatcher(height int64) *HeightMatcher { return &HeightMatcher{height: height} }
func (h *HeightMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	cur := int64(0)
	if state != nil {
		cur = int64(state.HeadData.Height)
	}
	if cur >= h.height {
		return SuccessResponse{}
	}
	return HeightResponse{h.height, cur}
}

type SlotHeightMatcher struct{ slotHeight int64 }

func NewSlotHeightMatcher(slotHeight int64) *SlotHeightMatcher {
	return &SlotHeightMatcher{slotHeight: slotHeight}
}
func (s *SlotHeightMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	cur := int64(0)
	if state != nil {
		cur = int64(state.HeadData.Slot)
	}
	if cur >= s.slotHeight {
		return SuccessResponse{}
	}
	return SlotHeightResponse{s.slotHeight, cur}
}

type LowerHeightPredictor func(upstreamId string, boundType protocol.LowerBoundType, timeOffset int64) int64

type LowerHeightMatcher struct {
	height     int64
	boundType  protocol.LowerBoundType
	timeOffset int64
	delta      int64
	predict    LowerHeightPredictor
}

func NewLowerHeightMatcher(height int64, boundType protocol.LowerBoundType, timeOffset, delta int64, predict LowerHeightPredictor) *LowerHeightMatcher {
	return &LowerHeightMatcher{height: height, boundType: boundType, timeOffset: timeOffset, delta: delta, predict: predict}
}
func (l *LowerHeightMatcher) Match(upId string, _ *protocol.UpstreamState) MatchResponse {
	predicted := int64(0)
	if l.predict != nil {
		predicted = l.predict(upId, l.boundType, l.timeOffset)
	}
	if predicted != 0 && l.height >= predicted-l.delta {
		return SuccessResponse{}
	}
	return LowerHeightResponse{l.height, predicted, l.boundType}
}

type SelectorAndMatcher struct{ matchers []Matcher }

func (a *SelectorAndMatcher) Match(upId string, state *protocol.UpstreamState) MatchResponse {
	responses := make([]MatchResponse, 0, len(a.matchers))
	for _, m := range a.matchers {
		r := m.Match(upId, state)
		if r.Type() != SuccessType {
			responses = append(responses, r)
		}
	}
	if len(responses) == 0 {
		return SuccessResponse{}
	}
	return MultiResponse{responses}
}

type SelectorOrMatcher struct{ matchers []Matcher }

func (o *SelectorOrMatcher) Match(upId string, state *protocol.UpstreamState) MatchResponse {
	responses := make([]MatchResponse, 0, len(o.matchers))
	for _, m := range o.matchers {
		r := m.Match(upId, state)
		if r.Type() == SuccessType {
			return SuccessResponse{}
		}
		responses = append(responses, r)
	}
	return MultiResponse{responses}
}

type SelectorNotMatcher struct{ matcher Matcher }

func (n *SelectorNotMatcher) Match(upId string, state *protocol.UpstreamState) MatchResponse {
	r := n.matcher.Match(upId, state)
	if r.Type() != SuccessType {
		return SuccessResponse{}
	}
	return NotMatchedResponse{r}
}

type UnsupportedSelectorMatcher struct{ reason string }

func (u *UnsupportedSelectorMatcher) Match(_ string, _ *protocol.UpstreamState) MatchResponse {
	return SelectorUnsupportedResponse{u.reason}
}
