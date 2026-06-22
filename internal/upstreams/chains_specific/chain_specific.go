package chains_specific

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
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

	// CapDetectors returns the chain's capability detectors, given the connectors and
	// methods discovered at the upstream level. The detectors are aggregated by a
	// caps.CapProcessor into the upstream's live capability set.
	CapDetectors(input caps.DetectorInput) []caps.CapDetector

	LowerBoundProcessor() lower_bounds.LowerBoundProcessor
	LabelsProcessor() labels.LabelsProcessor
	BlockProcessor() blocks.BlockProcessor
}
