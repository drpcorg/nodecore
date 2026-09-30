package protocol

import "slices"

// UpstreamIdLabel names the label selector that pins a request to upstreams
// by id. Elsewhere than at the top level or under AND, or without values, it
// is an ordinary label, which no upstream has.
const UpstreamIdLabel = "upstream_id"

// UpstreamPin restricts execution to explicit upstream IDs or one opaque group.
type UpstreamPin struct {
	Values []string
	Group  bool
}
type UpstreamPins []UpstreamPin

// SplitUpstreamPins extracts upstream_id and node_group_id constraints at the
// top level or under AND. Invalid group expressions become a gate that admits no
// upstream; ordinary selectors keep their existing semantics.
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
				pins = append(pins, UpstreamPin{Values: s.Values, Group: s.Name == NodeGroupLabel})
				continue
			}
		case RequestOrSelector, RequestNotSelector:
			if containsNodeGroup(selector) {
				// Group constraints cannot be weakened by OR or negation.
				pins = append(pins, UpstreamPin{Group: true})
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
	return (selector.Name == UpstreamIdLabel && len(selector.Values) > 0) || selector.Name == NodeGroupLabel
}

func containsNodeGroup(selector RequestSelector) bool {
	switch s := selector.(type) {
	case RequestLabelSelector:
		return s.Name == NodeGroupLabel
	case RequestAndSelector:
		return slices.ContainsFunc(s.Children, containsNodeGroup)
	case RequestOrSelector:
		return slices.ContainsFunc(s.Children, containsNodeGroup)
	case RequestNotSelector:
		return containsNodeGroup(s.Child)
	}
	return false
}

func hasUpstreamPins(selectors []RequestSelector) bool {
	for _, selector := range selectors {
		switch s := selector.(type) {
		case RequestLabelSelector:
			if isUpstreamPin(s) {
				return true
			}
		case RequestOrSelector, RequestNotSelector:
			if containsNodeGroup(selector) {
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
		if pin.Group {
			// Multiple groups in one selector would turn an attempt into a union.
			if len(pin.Values) != 1 {
				return false
			}
			want := pin.Values[0]
			if want != NodeGroupID(id, state, false) && want != NodeGroupID(id, state, true) {
				return false
			}
		} else if !slices.Contains(pin.Values, id) {
			return false
		}
	}
	return true
}

// Ids lists every pinned id once, in order.
func (p UpstreamPins) Ids() []string {
	var ids []string
	for _, list := range p {
		for _, id := range list.Values {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
