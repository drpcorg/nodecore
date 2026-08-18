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

const (
	AllInfoLabel     = "allInfo"
	PartialInfoLabel = "partialInfo"
)

// EthAllInfoLabelsDetector probes the HyperCore `info` endpoint via the
// rest-additional connector and emits a pair of complementary labels:
// "allInfo" is "true" when the upstream can serve the full info set (allMids
// succeeds), while "partialInfo" is "true" when it can serve only a subset of
// the info methods (allMids fails). Exactly one of the two is ever "true", so
// routing rules can select either capability directly. It is a no-op (returns
// nil) for non-Hyperliquid chains or when no rest-additional connector is
// configured.
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

	req := protocol.NewInternalUpstreamRestRequestWithBody("POST#/info", nil, allInfoProbeBody, e.chain)

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	infoLabels := map[string]string{
		AllInfoLabel:     "true",
		PartialInfoLabel: "false",
	}
	if resp.HasError() {
		infoLabels[AllInfoLabel] = "false"
		infoLabels[PartialInfoLabel] = "true"
		log.Error().Err(resp.GetError()).Msgf("allMids info probe failed for upstream '%s', marking %s=false and %s=true", e.upstreamId, AllInfoLabel, PartialInfoLabel)
	}
	return infoLabels
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
