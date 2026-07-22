package stellar_specific

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: stellar-rpc is HTTP POST JSON-RPC only, and
// Horizon's SSE streaming is out of v1 scope, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = fmt.Errorf("stellar: head subscriptions are not supported")

// NewStellarChainSpecificObject picks the specific object from the PRIMARY
// (internal-request) connector: rest means Horizon drives all accounting
// (head, health, chain validation, labels, lower bounds), anything else means
// stellar-rpc. When an upstream combines both APIs, the non-primary connector
// only serves methods - it takes no part in validations or calculations - and
// a warning is logged so the operator knows the split deployment (one
// upstream per API) is the one with full per-API accounting.
func NewStellarChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	allConnectors []connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) chains_specific.ChainSpecific {
	isHorizon := connector != nil && connector.GetType() == specs.RestConnector
	if len(allConnectors) > 1 {
		primary := "stellar-rpc"
		if isHorizon {
			primary = "horizon"
		}
		log.Warn().Msgf(
			"stellar upstream '%s' combines the stellar-rpc and horizon connectors: all validations and "+
				"calculations (head, health, chain validation, labels, lower bounds) are computed from the "+
				"primary %s API only; the other connector serves methods and nothing else. Deploy each API "+
				"as its own upstream to get independent accounting",
			upstreamId, primary,
		)
	}
	if isHorizon {
		return NewStellarHorizonChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options)
	}
	return NewStellarRpcChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options)
}

// stellarBaseChainSpecificObject holds the state and behavior shared by the
// stellar-rpc and Horizon objects; API-specific requests, parsing and
// validators live on the concrete types.
type stellarBaseChainSpecificObject struct {
	ctx             context.Context
	upstreamId      string
	connector       connectors.ApiConnector
	options         *chains.Options
	pollInterval    time.Duration
	internalTimeout time.Duration
	labelsDelay     time.Duration
	configuredChain *chains.ConfiguredChain
}

func newStellarBaseChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) stellarBaseChainSpecificObject {
	return stellarBaseChainSpecificObject{
		ctx:             ctx,
		upstreamId:      upstreamId,
		connector:       connector,
		options:         options,
		pollInterval:    pollInterval,
		internalTimeout: options.InternalTimeout,
		labelsDelay:     options.ValidationInterval * 5,
		configuredChain: configuredChain,
	}
}

func (s *stellarBaseChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	// stellar-rpc has no websocket transport and Horizon streams over SSE,
	// not ws, so no ws-derived caps can ever be asserted
	return nil
}

// newStellarBlockProcessor polls the finalized head with the generic block
// processor; SCP closes ledgers with immediate finality, so it tracks the
// same head the head processor sees, and there is no "safe" ledger concept.
func (s *stellarBaseChainSpecificObject) newStellarBlockProcessor(chainSpecific blocks.BlockChainSpecific) blocks.BlockProcessor {
	return blocks.NewBaseBlockProcessor(
		s.ctx,
		s.upstreamId,
		s.pollInterval,
		s.internalTimeout,
		s.options.FinalizedBlockDetectionDisabled(),
		true,
		s.connector,
		chainSpecific,
	)
}

func (s *stellarBaseChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (s *stellarBaseChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}
