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
