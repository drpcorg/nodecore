package emerald

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/samber/lo"
)

func SubMethodsToApi(methods []string) *dshackle.ChainEvent {
	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_SupportedSubscriptionsEvent{
			SupportedSubscriptionsEvent: &dshackle.SupportedSubscriptionsEvent{
				Subs: methods,
			},
		},
	}
}

func LabelsToApi(labels []upstreams.AggregatedLabels) *dshackle.ChainEvent {
	nodeDetails := lo.Map(labels, func(item upstreams.AggregatedLabels, index int) *dshackle.NodeDetails {
		apiLabels := lo.MapToSlice(item.Labels, func(key string, value string) *dshackle.Label {
			return &dshackle.Label{
				Name:  key,
				Value: value,
			}
		})

		return &dshackle.NodeDetails{
			Quorum: uint32(item.Amount),
			Labels: apiLabels,
		}
	})

	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_NodesEvent{
			NodesEvent: &dshackle.NodeDetailsEvent{
				Nodes: nodeDetails,
			},
		},
	}
}

func ChainStatusToApi(status protocol.AvailabilityStatus) *dshackle.ChainEvent {
	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_Status{
			Status: &dshackle.ChainStatus{
				Availability: availabilityStatusToApi(status),
			},
		},
	}
}

func SupportedMethodsToApi(methods []string) *dshackle.ChainEvent {
	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_SupportedMethodsEvent{
			SupportedMethodsEvent: &dshackle.SupportedMethodsEvent{
				Methods: methods,
			},
		},
	}
}

func LowerBoundsToApi(lowerBounds []protocol.LowerBoundData) *dshackle.ChainEvent {
	lowerBoundsApi := lo.Map(lowerBounds, func(item protocol.LowerBoundData, index int) *dshackle.LowerBound {
		return &dshackle.LowerBound{
			LowerBoundTimestamp: uint64(item.Timestamp),
			LowerBoundValue:     uint64(item.Bound),
			LowerBoundType:      lowerBoundTypeToApi(item.Type),
		}
	})

	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_LowerBoundsEvent{
			LowerBoundsEvent: &dshackle.LowerBoundEvent{
				LowerBounds: lowerBoundsApi,
			},
		},
	}
}

func BlocksToApi(blocks map[protocol.BlockType]protocol.Block) *dshackle.ChainEvent {
	blocksApi := make([]*dshackle.FinalizationData, len(blocks))
	i := 0
	for key, value := range blocks {
		blocksApi[i] = &dshackle.FinalizationData{Height: value.Height, Type: blockTypeToApi(key)}
		i++
	}

	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_FinalizationDataEvent{
			FinalizationDataEvent: &dshackle.FinalizationDataEvent{
				FinalizationData: blocksApi,
			},
		},
	}
}

func HeadToApi(head protocol.Block) *dshackle.ChainEvent {
	return &dshackle.ChainEvent{
		ChainEvent: &dshackle.ChainEvent_Head{
			Head: &dshackle.HeadEvent{
				Height:        head.Height,
				BlockId:       head.Hash.ToHex(),
				Slot:          head.Slot,
				ParentBlockId: head.ParentHash.ToHex(),
			},
		},
	}
}

func blockTypeToApi(blockType protocol.BlockType) dshackle.FinalizationType {
	switch blockType {
	case protocol.FinalizedBlock:
		return dshackle.FinalizationType_FINALIZATION_FINALIZED_BLOCK
	default:
		return dshackle.FinalizationType_FINALIZATION_UNSPECIFIED
	}
}

func lowerBoundTypeToApi(lowerBoundType protocol.LowerBoundType) dshackle.LowerBoundType {
	switch lowerBoundType {
	case protocol.TxBound:
		return dshackle.LowerBoundType_LOWER_BOUND_TX
	case protocol.SlotBound:
		return dshackle.LowerBoundType_LOWER_BOUND_SLOT
	case protocol.StateBound:
		return dshackle.LowerBoundType_LOWER_BOUND_STATE
	case protocol.ReceiptsBound:
		return dshackle.LowerBoundType_LOWER_BOUND_RECEIPTS
	case protocol.BlockBound:
		return dshackle.LowerBoundType_LOWER_BOUND_BLOCK
	default:
		return dshackle.LowerBoundType_LOWER_BOUND_UNSPECIFIED
	}
}

func availabilityStatusToApi(status protocol.AvailabilityStatus) dshackle.AvailabilityEnum {
	switch status {
	case protocol.Available:
		return dshackle.AvailabilityEnum_AVAIL_OK
	case protocol.Unavailable:
		return dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE
	case protocol.Immature:
		return dshackle.AvailabilityEnum_AVAIL_IMMATURE
	case protocol.Syncing:
		return dshackle.AvailabilityEnum_AVAIL_SYNCING
	default:
		return dshackle.AvailabilityEnum_AVAIL_UNKNOWN
	}
}
