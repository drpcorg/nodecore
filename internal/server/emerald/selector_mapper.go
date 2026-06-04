package emerald

import (
	"fmt"
	"math"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/dshackle"
)

func mapDshackleSelectors(selectors []*dshackle.Selector) []protocol.RequestSelector {
	if len(selectors) == 0 {
		return nil
	}
	result := make([]protocol.RequestSelector, 0, len(selectors))
	for _, selector := range selectors {
		result = append(result, mapDshackleSelector(selector))
	}
	return result
}

func mapDshackleSelector(selector *dshackle.Selector) protocol.RequestSelector {
	if selector == nil {
		return protocol.RequestAnySelector{}
	}
	switch s := selector.GetSelectorType().(type) {
	case *dshackle.Selector_LabelSelector:
		label := s.LabelSelector
		if label == nil || strings.TrimSpace(label.GetName()) == "" {
			return protocol.RequestAnySelector{}
		}
		return protocol.RequestLabelSelector{Name: label.GetName(), Values: compactStrings(label.GetValue())}
	case *dshackle.Selector_ExistsSelector:
		return protocol.RequestExistsSelector{Name: s.ExistsSelector.GetName()}
	case *dshackle.Selector_AndSelector:
		return protocol.RequestAndSelector{Children: mapDshackleSelectors(s.AndSelector.GetSelectors())}
	case *dshackle.Selector_OrSelector:
		return protocol.RequestOrSelector{Children: mapDshackleSelectors(s.OrSelector.GetSelectors())}
	case *dshackle.Selector_NotSelector:
		return protocol.RequestNotSelector{Child: mapDshackleSelector(s.NotSelector.GetSelector())}
	case *dshackle.Selector_HeightSelector:
		return mapDshackleHeightSelector(s.HeightSelector)
	case *dshackle.Selector_SlotHeightSelector:
		slotHeight := s.SlotHeightSelector.GetSlotHeight()
		if slotHeight < 0 {
			return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("slot height selector value %d is not supported", slotHeight)}
		}
		return protocol.RequestSlotHeightSelector{SlotHeight: slotHeight}
	case *dshackle.Selector_LowerHeightSelector:
		return mapDshackleLowerHeightSelector(s.LowerHeightSelector)
	case *dshackle.Selector_MinVersionSelector, *dshackle.Selector_MaxVersionSelector:
		return protocol.RequestUnsupportedSelector{Reason: "version selectors are not supported in nodecore routing"}
	default:
		return protocol.RequestUnsupportedSelector{Reason: "unsupported selector"}
	}
}

func mapDshackleHeightSelector(selector *dshackle.HeightSelector) protocol.RequestSelector {
	if selector == nil {
		return protocol.RequestAnySelector{}
	}
	switch selector.GetHeightOrNumber().(type) {
	case *dshackle.HeightSelector_Number:
		number := selector.GetNumber()
		if number > math.MaxInt64 {
			return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("height selector number %d exceeds max supported height %d", number, int64(math.MaxInt64))}
		}
		return protocol.RequestHeightSelector{Height: int64(number)}
	case *dshackle.HeightSelector_Tag:
		blockTag, ok := mapDshackleBlockTag(selector.GetTag())
		if !ok {
			return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("height selector tag %s is not supported", selector.GetTag().String())}
		}
		return protocol.RequestBlockTagSelector{Tag: blockTag}
	default:
		//nolint:staticcheck // Deprecated dshackle height field is still accepted for backwards compatibility.
		height := selector.GetHeight()
		if height == -1 {
			return protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}
		}
		if height < 0 {
			return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("height selector value %d is not supported", height)}
		}
		return protocol.RequestHeightSelector{Height: height}
	}
}

func mapDshackleBlockTag(tag dshackle.BlockTag) (protocol.RequestBlockTag, bool) {
	switch tag {
	case dshackle.BlockTag_LATEST:
		return protocol.BlockTagLatest, true
	case dshackle.BlockTag_SAFE:
		return protocol.BlockTagSafe, true
	case dshackle.BlockTag_FINALIZED:
		return protocol.BlockTagFinalized, true
	default:
		return 0, false
	}
}

func mapDshackleLowerHeightSelector(selector *dshackle.LowerHeightSelector) protocol.RequestSelector {
	if selector == nil {
		return protocol.RequestAnySelector{}
	}
	if selector.GetHeight() < 0 {
		return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("lower height selector value %d is not supported", selector.GetHeight())}
	}
	if selector.GetHeightDelta() < 0 {
		return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("lower height selector delta %d is not supported", selector.GetHeightDelta())}
	}
	boundType, ok := mapDshackleLowerBoundType(selector.GetLowerBoundType())
	if !ok {
		return protocol.RequestUnsupportedSelector{Reason: fmt.Sprintf("lower bound type %s is not supported", selector.GetLowerBoundType().String())}
	}
	return protocol.RequestLowerHeightSelector{
		Height:         selector.GetHeight(),
		LowerBoundType: boundType,
		TimeOffset:     selector.GetTimeOffset(),
		HeightDelta:    selector.GetHeightDelta(),
	}
}

func mapDshackleLowerBoundType(boundType dshackle.LowerBoundType) (protocol.LowerBoundType, bool) {
	switch boundType {
	case dshackle.LowerBoundType_LOWER_BOUND_SLOT:
		return protocol.SlotBound, true
	case dshackle.LowerBoundType_LOWER_BOUND_STATE:
		return protocol.StateBound, true
	case dshackle.LowerBoundType_LOWER_BOUND_BLOCK:
		return protocol.BlockBound, true
	case dshackle.LowerBoundType_LOWER_BOUND_TX:
		return protocol.TxBound, true
	case dshackle.LowerBoundType_LOWER_BOUND_RECEIPTS:
		return protocol.ReceiptsBound, true
	default:
		return protocol.UnknownBound, false
	}
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

type selectorSortSpec struct {
	kind      string
	boundType protocol.LowerBoundType
	offset    int64
}

func rejectConflictingSortSelectors(selectors []protocol.RequestSelector) ([]protocol.RequestSelector, error) {
	var current selectorSortSpec
	hasCurrent := false
	for _, selector := range selectors {
		spec, ok := requestSelectorSortSpec(selector)
		if !ok {
			continue
		}
		if hasCurrent && current != spec {
			return nil, fmt.Errorf("conflicting selector sort hints are not supported")
		}
		current = spec
		hasCurrent = true
	}
	return selectors, nil
}

func requestSelectorSortSpec(selector protocol.RequestSelector) (selectorSortSpec, bool) {
	switch s := selector.(type) {
	case protocol.RequestBlockTagSelector:
		return selectorSortSpec{kind: fmt.Sprintf("block:%d", s.Tag)}, true
	case protocol.RequestLowerHeightSelector:
		if s.Height == 0 {
			return selectorSortSpec{kind: "lower", boundType: s.LowerBoundType, offset: s.TimeOffset}, true
		}
	}
	return selectorSortSpec{}, false
}
