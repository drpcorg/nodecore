package eth_labels

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

// allInfoProbeBody is the cheapest parameterless HyperCore `info` request. It
// succeeds (HTTP 200) only on the public node and returns 422 on custom nodes,
// so it cleanly distinguishes upstreams that can serve the full `info` set.
var allInfoProbeBody = []byte(`{"type":"allMids"}`)

// EthAllInfoLabelsDetector probes the HyperCore `info` endpoint via the
// rest-additional connector and emits the "allInfo" label: "true" when the
// upstream can serve public-only info methods (allMids succeeds), "false"
// otherwise. It is a no-op (returns nil) for non-Hyperliquid chains or when no
// rest-additional connector is configured.
type EthAllInfoLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (e *EthAllInfoLabelsDetector) DetectLabels() map[string]string {
	if e.chain != chains.HYPERLIQUID || e.connector == nil {
		return nil
	}

	req := protocol.NewInternalUpstreamRestRequestWithBody("POST", "/info", allInfoProbeBody, e.chain)

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	value := "true"
	if resp.HasError() {
		value = "false"
		log.Error().Err(resp.GetError()).Msgf("allMids info probe failed for upstream '%s', marking allInfo=false", e.upstreamId)
	}
	return map[string]string{"allInfo": value}
}

func NewEthAllInfoLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthAllInfoLabelsDetector {
	if connector != nil && connector.GetType() != specs.RestAdditional {
		log.Warn().Msgf("hyperliquid info label probe only supported for restAdditional connector, it won't work for upstream '%s'", upstreamId)
	}
	return &EthAllInfoLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

var _ labels.LabelsDetector = (*EthAllInfoLabelsDetector)(nil)
