package eth_labels

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// HistoricalProofsLabel marks an upstream whose eth_getProof is backed by a historical
// proof store (op-reth --proofs-history), detected through debug_proofsSyncStatus.
const HistoricalProofsLabel = "historical_proofs"

type EthHistoricalProofsLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	internalTimeout time.Duration
	connector       connectors.ApiConnector
}

func NewEthHistoricalProofsLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthHistoricalProofsLabelsDetector {
	return &EthHistoricalProofsLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		internalTimeout: internalTimeout,
		connector:       connector,
	}
}

type proofsSyncStatusShape struct {
	Earliest json.RawMessage `json:"earliest"`
	Latest   json.RawMessage `json:"latest"`
}

// DetectLabels reports historical_proofs=true when debug_proofsSyncStatus answers with a
// window (even an empty one - the store exists), historical_proofs=false when the upstream
// definitely lacks the method, and nothing on transient or unparseable answers so the last
// verdict stands.
func (e *EthHistoricalProofsLabelsDetector) DetectLabels() map[string]string {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("debug_proofsSyncStatus", []any{}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect historical proofs of upstream '%s'", e.upstreamId)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		if protocol.ClassifyMethodAvailability(response.GetError()) == protocol.MethodNotAvailable {
			return map[string]string{HistoricalProofsLabel: "false"}
		}
		log.Warn().Err(response.GetError()).Msgf("unable to detect historical proofs of upstream '%s'", e.upstreamId)
		return nil
	}

	status := proofsSyncStatusShape{}
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil || len(status.Earliest) == 0 || len(status.Latest) == 0 {
		log.Warn().Msgf("unable to parse debug_proofsSyncStatus of upstream '%s'", e.upstreamId)
		return nil
	}
	return map[string]string{HistoricalProofsLabel: "true"}
}

var _ labels.LabelsDetector = (*EthHistoricalProofsLabelsDetector)(nil)
