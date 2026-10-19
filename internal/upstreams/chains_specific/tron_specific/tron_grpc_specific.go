package tron_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/tron_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds/tron_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/tron_validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

// TronGrpcSpecific drives a tron upstream through java-tron's gRPC API when
// grpc is the upstream's only plain connector. Every probe is a unary call on
// protocol.Wallet and mirrors the HTTP probe of TronRestSpecific - the HTTP
// API is a JSON rendering of the same protobuf messages.
type TronGrpcSpecific struct {
	ctx          context.Context
	upstreamId   string
	pollInterval time.Duration
	connector    connectors.ApiConnector
	chain        *chains.ConfiguredChain
	options      *chains.Options
}

func (t *TronGrpcSpecific) BlockProcessor() blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
		t.ctx,
		t.upstreamId,
		t.pollInterval,
		t.options.InternalTimeout,
		t.options.FinalizedBlockDetectionDisabled(),
		t.options.SafeBlockDetectionDisabled(),
		t.connector,
		t,
	)
}

func (t *TronGrpcSpecific) GetLatestBlock(ctx context.Context) (protocol.Block, error) {
	raw, err := specific_helpers.FetchTronGrpcNowBlock(ctx, t.connector, t.chain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return t.ParseBlock(raw)
}

// GetFinalizedBlock reads the solidified height out of GetNodeInfo, the same
// "Num:N,ID:..." string the HTTP /wallet/getnodeinfo reports.
func (t *TronGrpcSpecific) GetFinalizedBlock(ctx context.Context) (protocol.Block, error) {
	nodeInfo, err := specific_helpers.FetchTronGrpcNodeInfo(ctx, t.connector, t.chain.Chain)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	height, err := specific_helpers.ParseTronSolidityHeight(nodeInfo.GetSolidityBlock())
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	return protocol.NewBlockWithHeight(height), nil
}

// ParseBlock expects a serialized BlockExtention. blockid and parentHash are
// the raw hash bytes, so they reduce to the same HashId the REST (bare hex)
// and json-rpc (0x hex) block parsers produce for the same block.
func (t *TronGrpcSpecific) ParseBlock(blockBytes []byte) (protocol.Block, error) {
	block, err := specific_helpers.ParseTronGrpcBlock(blockBytes)
	if err != nil {
		return protocol.ZeroBlock{}, err
	}
	header := block.GetBlockHeader().GetRawData()
	if header == nil {
		return protocol.ZeroBlock{}, fmt.Errorf("tron grpc block has no header")
	}
	return protocol.NewBlock(
		uint64(header.GetNumber()),
		0,
		blockchain.NewHashIdFromBytes(block.GetBlockid()),
		blockchain.NewHashIdFromBytes(header.GetParentHash()),
	), nil
}

func (t *TronGrpcSpecific) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, blocks.ErrUnsupportedHeadSubscriptions
}

func (t *TronGrpcSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, blocks.ErrUnsupportedHeadSubscriptions
}

func (t *TronGrpcSpecific) HealthValidators() []validations.Validator[protocol.AvailabilityStatus] {
	validators := make([]validations.Validator[protocol.AvailabilityStatus], 0, 2)
	if *t.options.ValidatePeers {
		validators = append(validators, tron_validations.NewTronGrpcPeersValidator(t.upstreamId, t.chain.Chain, t.connector, t.options))
	}
	if *t.options.ValidateSyncing {
		validators = append(validators, tron_validations.NewTronGrpcSyncingValidator(t.upstreamId, t.chain, t.connector, t.options.InternalTimeout))
	}
	return validators
}

func (t *TronGrpcSpecific) SettingsValidators() []validations.Validator[validations.ValidationSettingResult] {
	return nil
}

// CapDetectors returns nil: the tron spec declares no websocket connector, so
// there is no capability to detect.
func (t *TronGrpcSpecific) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

func (t *TronGrpcSpecific) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	detectors := []lower_bounds.LowerBoundDetector{
		tron_bounds.NewTronGrpcLowerBoundDetector(t.upstreamId, t.chain.Chain, t.options.InternalTimeout, t.connector),
	}
	return lower_bounds.NewGenericLowerBoundProcessor(t.ctx, t.upstreamId, t.chain.AverageRemoveSpeed(), detectors)
}

func (t *TronGrpcSpecific) LabelsProcessor() labels.LabelsProcessor {
	labelsDetectors := []labels.LabelsDetector{
		labels.NewClientLabelDetectorHandler(
			t.upstreamId,
			t.connector,
			tron_labels.NewTronGrpcClientLabelsDetector(t.chain.Chain),
			t.options.InternalTimeout,
		),
	}
	return labels.NewGenericLabelsProcessor(t.ctx, t.upstreamId, labelsDetectors, t.options.ValidationInterval*5)
}

// MethodsProcessor returns nil: java-tron exposes no way to ask which methods
// it implements, so the upstream keeps the full method set its spec declares.
func (t *TronGrpcSpecific) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

func (t *TronGrpcSpecific) PauseHeadWhileSyncing() bool {
	return false
}

func newTronGrpcSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (*TronGrpcSpecific, error) {
	if connector == nil {
		return nil, errors.New("no connector specified")
	}
	if connector.GetType() != specs.GrpcConnector {
		return nil, fmt.Errorf("tron grpc specific supports only the grpc connector but not %s", connector.GetType())
	}
	return &TronGrpcSpecific{
		ctx:          ctx,
		upstreamId:   upstreamId,
		connector:    connector,
		chain:        chain,
		options:      options,
		pollInterval: pollInterval,
	}, nil
}

var _ chains_specific.ChainSpecific = (*TronGrpcSpecific)(nil)
