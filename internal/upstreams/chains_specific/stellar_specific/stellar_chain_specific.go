package stellar_specific

import (
	"context"
	"errors"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// NewStellarChainSpecificObject picks the flavor from the PRIMARY
// (internal-request) connector: rest means Horizon drives all accounting
// (head, health, chain validation, labels, lower bounds), anything else means
// stellar-rpc.
//
// The factory derives the primary connector from
// conf.GetBestConnector(config.DefaultMode) - DefaultMode is hardcoded at that
// call site - so it is always the lowest ApiConnectorType ordinal. json-rpc
// sorts before rest, which means a combined upstream is driven by stellar-rpc
// in every upstream mode and Horizon can never drive a combined one. That is
// fine here: both APIs are front-ends of the same stellar node, so combining
// them on one upstream is a normal deployment and the non-primary connector
// simply serves methods.
func NewStellarChainSpecificObject(
	ctx context.Context,
	configuredChain *chains.ConfiguredChain,
	upstreamId string,
	connector connectors.ApiConnector,
	pollInterval time.Duration,
	options *chains.Options,
) chains_specific.ChainSpecific {
	if connector != nil && connector.GetType() == specs.RestConnector {
		return NewStellarHorizonChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options)
	}
	return NewStellarRpcChainSpecificObject(ctx, configuredChain, upstreamId, connector, pollInterval, options)
}

// errUnsupportedHeadSubscriptions is returned by SubscribeHeadRequest and
// ParseSubscriptionBlock: stellar-rpc is HTTP POST JSON-RPC only, and Horizon's
// SSE streaming is out of scope, so head tracking is poll-only.
var errUnsupportedHeadSubscriptions = errors.New("stellar: head subscriptions are not supported")

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

// CapDetectors returns nil: stellar-rpc has no websocket transport and Horizon
// streams over SSE rather than ws, so no ws-derived cap can ever be asserted.
func (s *stellarBaseChainSpecificObject) CapDetectors(_ caps.DetectorInput) []caps.CapDetector {
	return nil
}

// MethodsProcessor returns nil: neither API exposes a way to ask a node which
// methods it implements, so upstreams keep the full method set their spec
// declares.
func (s *stellarBaseChainSpecificObject) MethodsProcessor() methods.MethodsProcessor {
	return nil
}

func (s *stellarBaseChainSpecificObject) ParseSubscriptionBlock(_ []byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, errUnsupportedHeadSubscriptions
}

func (s *stellarBaseChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return nil, errUnsupportedHeadSubscriptions
}

// newStellarBlockProcessor polls the finalized head with the generic block
// processor. SCP closes ledgers with immediate finality, so it tracks the same
// ledger the head processor sees, and there is no "safe" ledger concept - safe
// block detection is disabled unconditionally.
func (s *stellarBaseChainSpecificObject) newStellarBlockProcessor(chainSpecific blocks.BlockChainSpecific) blocks.BlockProcessor {
	return blocks.NewGenericBlockProcessor(
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
