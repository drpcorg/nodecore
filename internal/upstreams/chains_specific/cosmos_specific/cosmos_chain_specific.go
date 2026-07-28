package cosmos_specific

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/tendermint_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

// NewCosmosSpecific picks the flavor from the primary (internal-request)
// connector. A cosmos node exposes two independent APIs - the CometBFT RPC on
// 26657 and the SDK LCD on 1317 - and either one can carry the full set of
// probes nodecore needs, so an upstream may be configured with one or both.
func NewCosmosSpecific(
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
	case specs.TendermintConnector:
		return tendermint_specific.NewTendermintSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	case specs.RestConnector:
		return newCosmosRestSpecific(ctx, upstreamId, connector, chain, pollInterval, options)
	default:
		return nil, fmt.Errorf(
			"cosmos specific supports only tendermint or rest connector but not %s",
			connector.GetType(),
		)
	}
}
