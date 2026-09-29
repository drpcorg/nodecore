package protocol

import "slices"

// UpstreamIdLabel names the label selector that pins a request to upstreams
// by id. Elsewhere than at the top level or under AND, or without values, it
// is an ordinary label, which no upstream has.
const UpstreamIdLabel = "upstream_id"

// UpstreamPins are the id lists an upstream must be in to serve the request.
type UpstreamPins [][]string

// SplitUpstreamPins takes out the upstream_id selectors every serving upstream
// must match: the top-level ones and those under AND. Selectors without pins
// come back as they are.
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

func (p UpstreamPins) Admits(upstreamId string) bool {
	for _, ids := range p {
		if !slices.Contains(ids, upstreamId) {
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
