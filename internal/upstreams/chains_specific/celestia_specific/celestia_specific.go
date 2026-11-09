package celestia_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/cosmos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tendermint_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
)

// NewCelestiaSpecific picks the flavor from the connector it is given. A celestia
// chain is served by two kinds of nodes: the DA node (celestia-node) exposes its
// own JSON-RPC API (header.*, blob.*, share.*), while the consensus node
// (celestia-app) is a regular cosmos-sdk node with the CometBFT RPC, the LCD
// REST and the SDK gRPC. The DA header height is the consensus height, so both
// kinds of upstreams feed one coherent chain head. The json-rpc and websocket
// connectors get the DA specific (the DA node serves the same API over both;
// its websocket carries the go-jsonrpc channel subscriptions); tendermint, rest
// and grpc get the cosmos specifics, which validate the chain against the cosmos
// chain id (celestia, mocha-…).
func NewCelestiaSpecific(
	ctx context.Context,
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	pollInterval time.Duration,
	options *chains.Options,
) (chains_specific.ChainSpecific, error) {
	if connector == nil {
		return nil, errors.New("no connector specified")
	}
	switch connector.GetType() {
	case specs.JsonRpcConnector, specs.WebsocketConnector:
		return NewCelestiaChainSpecificObject(ctx, chain, upstreamId, connector, pollInterval, options), nil
	case specs.TendermintConnector:
		return tendermint_specific.NewTendermintSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	case specs.RestConnector:
		return cosmos_specific.NewCosmosRestSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	case specs.GrpcConnector:
		return cosmos_specific.NewCosmosGrpcSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	default:
		return nil, fmt.Errorf(
			"celestia specific supports only json-rpc, websocket, tendermint, rest or grpc connector but not %s",
			connector.GetType(),
		)
	}
}
