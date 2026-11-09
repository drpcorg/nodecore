package flow

import (
	"strings"

	"github.com/Masterminds/semver/v3"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/rs/zerolog/log"
)

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

// CapMatcher passes upstreams that advertise cap. method names the subscription
// the check is made for, so the failure reads like any other unsupported method.
type CapMatcher struct {
	cap    protocol.Cap
	method string
}

func NewCapMatcher(cap protocol.Cap, method string) *CapMatcher {
	return &CapMatcher{cap: cap, method: method}
}

func (c *CapMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if state.Caps != nil && state.Caps.ContainsOne(c.cap) {
		return SuccessResponse{}
	}
	return MethodResponse{method: c.method}
}

var _ Matcher = (*CapMatcher)(nil)

// matcherFilter adapts a Matcher to the supervisor's FilterUpstream: an upstream
// passes when the matcher succeeds. Used to build filtered head feeds.
func matcherFilter(m Matcher) upstreams.FilterUpstream {
	return func(upId string, state *protocol.UpstreamState) bool {
		return m.Match(upId, state).Type() == SuccessType
	}
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

// VersionRangeMatcher admits an upstream whose client_version label parses as
// a semver within [min, max]. An empty bound is open. A missing or unparseable
// upstream version never matches, mirroring the dshackle matcher.
type VersionRangeMatcher struct {
	minRaw, maxRaw string
	min, max       *semver.Version
	valid          bool
}

func NewVersionRangeMatcher(minVersion, maxVersion string) *VersionRangeMatcher {
	matcher := &VersionRangeMatcher{minRaw: minVersion, maxRaw: maxVersion, valid: true}
	if minVersion != "" {
		matcher.min = parseClientVersion(minVersion)
		matcher.valid = matcher.valid && matcher.min != nil
	}
	if maxVersion != "" {
		matcher.max = parseClientVersion(maxVersion)
		matcher.valid = matcher.valid && matcher.max != nil
	}
	return matcher
}

func (v *VersionRangeMatcher) Match(_ string, state *protocol.UpstreamState) MatchResponse {
	if !v.valid || state == nil || state.Labels == nil {
		return VersionRangeResponse{v.minRaw, v.maxRaw}
	}
	raw, ok := state.Labels.GetLabel("client_version")
	if !ok {
		return VersionRangeResponse{v.minRaw, v.maxRaw}
	}
	actual := parseClientVersion(raw)
	if actual == nil {
		return VersionRangeResponse{v.minRaw, v.maxRaw}
	}
	if v.min != nil && actual.LessThan(v.min) {
		return VersionRangeResponse{v.minRaw, v.maxRaw}
	}
	if v.max != nil && actual.GreaterThan(v.max) {
		return VersionRangeResponse{v.minRaw, v.maxRaw}
	}
	return SuccessResponse{}
}

// parseClientVersion reads a strict semver, tolerating the "v" prefix that
// clients such as geth report. It returns nil when the value is not a semver.
func parseClientVersion(raw string) *semver.Version {
	version, err := semver.StrictNewVersion(strings.TrimPrefix(raw, "v"))
	if err != nil {
		return nil
	}
	return version
}

// GroupLabelsLookup returns the config group-labels of an upstream, or nil
// when the upstream is unknown.
type GroupLabelsLookup func(upstreamId string) mapset.Set[string]

// GroupLabelMatcher admits an upstream carrying at least one of labels among
// its config group-labels. Group-labels live on the upstream's config, not in
// its state, so they are resolved by id.
type GroupLabelMatcher struct {
	labels []string
	lookup GroupLabelsLookup
}

func NewGroupLabelMatcher(labels []string, lookup GroupLabelsLookup) *GroupLabelMatcher {
	return &GroupLabelMatcher{labels: labels, lookup: lookup}
}

func (g *GroupLabelMatcher) Match(upstreamId string, _ *protocol.UpstreamState) MatchResponse {
	if g.lookup != nil {
		if upstreamLabels := g.lookup(upstreamId); upstreamLabels != nil && upstreamLabels.ContainsAny(g.labels...) {
			return SuccessResponse{}
		}
	}
	return GroupLabelResponse{g.labels}
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
