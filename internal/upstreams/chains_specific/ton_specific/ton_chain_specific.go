package ton_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: neither the v2 HTTP API nor the v3 indexer has any
// subscription transport, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("ton: head subscriptions are not supported")

// NewTonChainSpecificObject picks the specific object from the PRIMARY
// (internal-request) connector: rest-indexer means the v3 API drives all
// accounting (head, health, chain validation, labels), anything else means
// v2. When an upstream combines both APIs, the non-primary connector only
// serves methods - it takes no part in validations or calculations - and a
// warning is logged so the operator knows the split deployment (one upstream
// per API) is the one with full per-API accounting.
func NewTonChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	options *chains.Options,
) chains_specific.ChainSpecific {
	isV3 := connector != nil && connector.GetType() == specs.RestIndexer
	if len(allConnectors) > 1 {
		primary := "v2"
		if isV3 {
			primary = "v3"
		}
		log.Warn().Msgf(
			"ton upstream '%s' combines the v2 and v3 connectors: all validations and calculations "+
				"(head, health, chain validation, labels) are computed from the primary "+
				"%s API only; the other connector serves methods and nothing else. Deploy each API as "+
				"its own upstream to get independent accounting",
			upstreamId, primary,
		)
	}
	if isV3 {
		return NewTonV3ChainSpecificObject(ctx, configuredChain, upstreamId, connector, options)
	}
	return NewTonV2ChainSpecificObject(ctx, configuredChain, upstreamId, connector, options)
}

// tonBaseChainSpecificObject holds the state and behavior shared by the v2
// and v3 objects; flavor-specific requests, parsing and validators live on
// the concrete types.
type tonBaseChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func newTonBaseChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	options *chains.Options,
) tonBaseChainSpecificObject {
	return tonBaseChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

// BlockProcessor is nil: the masterchain head is BFT-final the moment it is
// published, so there is no separate finalized head to poll.
func (t *tonBaseChainSpecificObject) BlockProcessor() blocks.BlockProcessor {
	return nil
}

func (t *tonBaseChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	// no ws transport in either TON API, so no ws-derived caps can ever be asserted
	return nil
}

// LowerBoundProcessor is nil: lower bound detection for ton is omitted,
// matching dshackle.
func (t *tonBaseChainSpecificObject) LowerBoundProcessor() lower_bounds.LowerBoundProcessor {
	return nil
}

func (t *tonBaseChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (t *tonBaseChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

type tonBlockIdExt struct {
	Seqno    uint64 `json:"seqno"`
	RootHash string `json:"root_hash"`
}

// tonBlockFromIdExt converts a masterchain block id into a protocol block.
// The base64 root_hash is used verbatim as the block id; the parent hash is
// left empty (dshackle fills it with a random string - EmptyHash is the
// honest equivalent, see the near/ripple precedent).
func tonBlockFromIdExt(last tonBlockIdExt) protocol.Block {
	return protocol.NewBlock(
		last.Seqno,
		0,
		blockchain.NewHashIdFromString(last.RootHash),
		blockchain.EmptyHash,
	)
}
