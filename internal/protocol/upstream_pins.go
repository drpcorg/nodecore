package protocol

import "slices"

// UpstreamIdLabel names the label selector that pins a request to upstreams
// by id. Elsewhere than at the top level or under AND, or without values, it
// is an ordinary label, which no upstream has.
const UpstreamIdLabel = "upstream_id"

// UpstreamPins intersects the allowed ID sets extracted from request selectors.
type UpstreamPins [][]string

// SplitUpstreamPins extracts upstream_id constraints at the top level or under
// AND; ordinary selectors keep their existing semantics.
func SplitUpstreamPins(selectors []RequestSelector) (UpstreamPins, []RequestSelector) {
	if !hasUpstreamPins(selectors) {
		return nil, selectors
	}
	var pins UpstreamPins
	rest := make([]RequestSelector, 0, len(selectors))
	for _, selector := range selectors {
		switch s := selector.(type) {
		case RequestLabelSelector:
			if isUpstreamPin(s) {
				pins = append(pins, s.Values)
				continue
			}
		case RequestAndSelector:
			childPins, children := SplitUpstreamPins(s.Children)
			pins = append(pins, childPins...)
			selector = RequestAndSelector{Children: children}
		}
		rest = append(rest, selector)
	}
	return pins, rest
}

func isUpstreamPin(selector RequestLabelSelector) bool {
	return selector.Name == UpstreamIdLabel && len(selector.Values) > 0
}

func hasUpstreamPins(selectors []RequestSelector) bool {
	for _, selector := range selectors {
		switch s := selector.(type) {
		case RequestLabelSelector:
			if isUpstreamPin(s) {
				return true
			}
		case RequestAndSelector:
			if hasUpstreamPins(s.Children) {
				return true
			}
		}
	}
	return false
}

func (p UpstreamPins) Pinned() bool {
	return len(p) > 0
}

func (p UpstreamPins) Matches(id string, state *UpstreamState) bool {
	if state == nil {
		return false
	}
	for _, pin := range p {
		if !slices.Contains(pin, id) {
			return false
		}
	}
	return true
}

// Ids lists every pinned id once, in order.
func (p UpstreamPins) Ids() []string {
	var ids []string
	for _, list := range p {
		for _, id := range list {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
