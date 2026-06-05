package emerald

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapDshackleSelectors(t *testing.T) {
	selectors := mapDshackleSelectors([]*dshackle.Selector{
		{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{Name: "region", Value: []string{"us", ""}}}},
		{SelectorType: &dshackle.Selector_ExistsSelector{ExistsSelector: &dshackle.ExistsSelector{Name: "archive"}}},
		{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Number{Number: 100}}}},
		{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_FINALIZED}}}},
		{SelectorType: &dshackle.Selector_SlotHeightSelector{SlotHeightSelector: &dshackle.SlotHeightSelector{SlotHeight: 55}}},
		{SelectorType: &dshackle.Selector_LowerHeightSelector{LowerHeightSelector: &dshackle.LowerHeightSelector{Height: 42, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_RECEIPTS, TimeOffset: 7, HeightDelta: 3}}},
	})

	label := selectors[0].(protocol.RequestLabelSelector)
	assert.Equal(t, "region", label.Name)
	assert.Equal(t, []string{"us"}, label.Values)
	assert.Equal(t, protocol.RequestExistsSelector{Name: "archive"}, selectors[1])
	assert.Equal(t, protocol.RequestHeightSelector{Height: 100}, selectors[2])
	assert.Equal(t, protocol.RequestBlockTagSelector{Tag: protocol.BlockTagFinalized}, selectors[3])
	assert.Equal(t, protocol.RequestSlotHeightSelector{SlotHeight: 55}, selectors[4])
	assert.Equal(t, protocol.RequestLowerHeightSelector{Height: 42, LowerBoundType: protocol.ReceiptsBound, TimeOffset: 7, HeightDelta: 3}, selectors[5])
}

func TestMapDshackleSelectorsCompositionAndUnsupported(t *testing.T) {
	selector := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_AndSelector{AndSelector: &dshackle.AndSelector{Selectors: []*dshackle.Selector{
		{SelectorType: &dshackle.Selector_OrSelector{OrSelector: &dshackle.OrSelector{Selectors: []*dshackle.Selector{
			{SelectorType: &dshackle.Selector_ExistsSelector{ExistsSelector: &dshackle.ExistsSelector{Name: "a"}}},
		}}}},
		{SelectorType: &dshackle.Selector_NotSelector{NotSelector: &dshackle.NotSelector{Selector: &dshackle.Selector{SelectorType: &dshackle.Selector_ExistsSelector{ExistsSelector: &dshackle.ExistsSelector{Name: "b"}}}}}},
	}}}})

	and, ok := selector.(protocol.RequestAndSelector)
	require.True(t, ok)
	require.Len(t, and.Children, 2)
	_, ok = and.Children[0].(protocol.RequestOrSelector)
	assert.True(t, ok)
	_, ok = and.Children[1].(protocol.RequestNotSelector)
	assert.True(t, ok)

	unsupported := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_MaxVersionSelector{MaxVersionSelector: &dshackle.MaxVersionSelector{Version: "1.0.0"}}})
	_, ok = unsupported.(protocol.RequestUnsupportedSelector)
	assert.True(t, ok)
}

func TestMapDshackleHeightSelectorFailsClosedForUnsupportedValues(t *testing.T) {
	tooLarge := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Number{Number: 1 << 63}}}})
	unsupported := tooLarge.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "exceeds max supported height")

	pending := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_PENDING}}}})
	unsupported = pending.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "PENDING is not supported")

	unknown := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_UNKNOWN}}}})
	unsupported = unknown.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "UNKNOWN is not supported")
}

func TestMapDshackleHeightSelectorFailsClosedForNegativeDeprecatedHeight(t *testing.T) {
	legacyLatest := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{Height: -1}}})
	assert.Equal(t, protocol.RequestBlockTagSelector{Tag: protocol.BlockTagLatest}, legacyLatest)

	negative := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{Height: -2}}})
	unsupported := negative.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "-2")
}

func TestMapDshackleSlotHeightSelectorFailsClosedForNegativeValue(t *testing.T) {
	negative := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_SlotHeightSelector{SlotHeightSelector: &dshackle.SlotHeightSelector{SlotHeight: -1}}})
	unsupported := negative.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "slot height")
}

func TestMapDshackleLowerHeightSelectorFailsClosedForNegativeValues(t *testing.T) {
	negativeHeight := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_LowerHeightSelector{LowerHeightSelector: &dshackle.LowerHeightSelector{Height: -1, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_STATE, HeightDelta: 200}}})
	unsupported := negativeHeight.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "lower height selector value -1")

	negativeDelta := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_LowerHeightSelector{LowerHeightSelector: &dshackle.LowerHeightSelector{Height: 100, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_STATE, HeightDelta: -1}}})
	unsupported = negativeDelta.(protocol.RequestUnsupportedSelector)
	assert.Contains(t, unsupported.Reason, "lower height selector delta -1")

	negativeTimeOffset := mapDshackleSelector(&dshackle.Selector{SelectorType: &dshackle.Selector_LowerHeightSelector{LowerHeightSelector: &dshackle.LowerHeightSelector{Height: 100, LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_STATE, TimeOffset: -30}}})
	assert.Equal(t, protocol.RequestLowerHeightSelector{Height: 100, LowerBoundType: protocol.StateBound, TimeOffset: -30}, negativeTimeOffset)
}
