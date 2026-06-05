package chains_specific

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
)

type ChainSpecific interface {
	GetLatestBlock(ctx context.Context) (protocol.Block, error)
	GetFinalizedBlock(context.Context) (protocol.Block, error)

	ParseBlock([]byte) (protocol.Block, error)
	ParseSubscriptionBlock(data []byte) (protocol.Block, error)

	SubscribeHeadRequest() (protocol.RequestHolder, error)

	HealthValidators() []validations.Validator[protocol.AvailabilityStatus]
	SettingsValidators() []validations.Validator[validations.ValidationSettingResult]

	LowerBoundProcessor() lower_bounds.LowerBoundProcessor
	LabelsProcessor() labels.LabelsProcessor
	BlockProcessor() blocks.BlockProcessor
}
