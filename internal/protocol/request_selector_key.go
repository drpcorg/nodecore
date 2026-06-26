package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// Key implementations produce a deterministic, order-independent identity for
// each RequestSelector. and/or groups sort their children so that logically
// equal trees collide regardless of ordering. See RequestSelector.Key.

func (RequestAnySelector) Key() string {
	return "any"
}

func (s RequestLabelSelector) Key() string {
	values := append([]string(nil), s.Values...)
	sort.Strings(values)
	return fmt.Sprintf("label(%s=%s)", s.Name, strings.Join(values, "|"))
}

func (s RequestExistsSelector) Key() string {
	return fmt.Sprintf("exists(%s)", s.Name)
}

func (s RequestAndSelector) Key() string {
	return fmt.Sprintf("and(%s)", selectorGroupKey(s.Children))
}

func (s RequestOrSelector) Key() string {
	return fmt.Sprintf("or(%s)", selectorGroupKey(s.Children))
}

func (s RequestNotSelector) Key() string {
	return fmt.Sprintf("not(%s)", s.Child.Key())
}

func (s RequestHeightSelector) Key() string {
	return fmt.Sprintf("height(%d)", s.Height)
}

func (s RequestBlockTagSelector) Key() string {
	return fmt.Sprintf("tag(%d)", s.Tag)
}

func (s RequestSlotHeightSelector) Key() string {
	return fmt.Sprintf("slot(%d)", s.SlotHeight)
}

func (s RequestLowerHeightSelector) Key() string {
	return fmt.Sprintf("lower(%d,%d,%d,%d)", s.Height, s.LowerBoundType, s.TimeOffset, s.HeightDelta)
}

func (s RequestUnsupportedSelector) Key() string {
	return fmt.Sprintf("unsupported(%s)", s.Reason)
}

func selectorGroupKey(children []RequestSelector) string {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		parts = append(parts, child.Key())
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// LabelCacheKey reduces a selector tree to its node-class identity for cache
// keying. Some chains expose distinct node classes (selected via label
// selectors) that return different responses for the same method+params, so the
// chosen class must be part of the cache key or one class's response can be
// served to a request that pinned another.
//
// Only label/exists selectors carry a node-class constraint; every other
// selector (height, block tag, slot, lower bound, any, unsupported) is treated
// as ⊤ — it admits every class and is irrelevant to which class answers. The
// reduction walks the boolean structure over that label dimension only:
// AND intersects (⊤ children drop out), OR unions (any ⊤ child makes the whole
// union ⊤), NOT complements. An empty result means the request is unconstrained
// over node class and shares the default cache entry, leaving keys for
// label-free traffic byte-identical to before.
func LabelCacheKey(selectors []RequestSelector) string {
	// Top-level selectors are applied conjunctively, like an implicit AND.
	return labelClassKey(RequestAndSelector{Children: selectors})
}

func labelClassKey(selector RequestSelector) string {
	switch s := selector.(type) {
	case RequestLabelSelector:
		return s.Key()
	case RequestExistsSelector:
		return s.Key()
	case RequestAndSelector:
		// Intersection: ⊤ (empty-key) children drop out.
		parts := labelClassParts(s.Children)
		switch len(parts) {
		case 0:
			return ""
		case 1:
			return parts[0]
		default:
			return fmt.Sprintf("and(%s)", strings.Join(parts, "|"))
		}
	case RequestOrSelector:
		// Union: a single ⊤ child makes the whole union ⊤.
		parts := make([]string, 0, len(s.Children))
		for _, child := range s.Children {
			key := labelClassKey(child)
			if key == "" {
				return ""
			}
			parts = append(parts, key)
		}
		switch len(parts) {
		case 0:
			return ""
		case 1:
			return parts[0]
		default:
			sort.Strings(parts)
			return fmt.Sprintf("or(%s)", strings.Join(parts, "|"))
		}
	case RequestNotSelector:
		// Complement of ⊤ is the empty set (no class) — irrelevant for keying,
		// so it too collapses to the shared entry.
		key := labelClassKey(s.Child)
		if key == "" {
			return ""
		}
		return fmt.Sprintf("not(%s)", key)
	default:
		// RequestAnySelector, height/slot/tag/lower-bound, unsupported, nil:
		// no node-class constraint.
		return ""
	}
}

func labelClassParts(children []RequestSelector) []string {
	parts := make([]string, 0, len(children))
	for _, child := range children {
		if key := labelClassKey(child); key != "" {
			parts = append(parts, key)
		}
	}
	sort.Strings(parts)
	return parts
}
