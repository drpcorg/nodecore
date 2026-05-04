package lower_bounds

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const aztecPeriod = 5 * time.Minute

// AztecLowerBoundDetector reports the lowest L2 block for which the upstream
// still has state available, by reading oldestHistoricBlockNumber from
// node_getWorldStateSyncStatus (one RPC per refresh - the world-state
// synchronizer publishes this number directly, no binary-search probing
// needed).
//
// On transient errors (the public Aztec endpoint occasionally returns code 19
// on this method) we fall back to StateBound=1 since Aztec full nodes are
// archive by default. Subsequent refresh ticks will retry and pick up the real
// prune boundary if the node is actually pruning.
type AztecLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func NewAztecLowerBoundDetector(
	upstreamId string,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AztecLowerBoundDetector {
	return &AztecLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (a *AztecLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	bound := a.fetchOldestHistoric()
	if bound < 1 {
		bound = 1
	}
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(bound, protocol.StateBound),
	}, nil
}

func (a *AztecLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound}
}

func (a *AztecLowerBoundDetector) Period() time.Duration {
	return aztecPeriod
}

// fetchOldestHistoric returns the upstream's oldestHistoricBlockNumber, or 1
// (the archive-node default) on any error or unparseable response.
func (a *AztecLowerBoundDetector) fetchOldestHistoric() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getWorldStateSyncStatus", []interface{}{}, chains.AZTEC_MAINNET)
	if err != nil {
		log.Debug().Err(err).Msgf("aztec upstream '%s' couldn't build world state sync request, defaulting to STATE=1", a.upstreamId)
		return 1
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		log.Debug().Err(response.GetError()).Msgf("aztec upstream '%s' world state sync status unavailable, defaulting to STATE=1", a.upstreamId)
		return 1
	}

	var status worldStateSyncStatus
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		log.Debug().Err(err).Msgf("aztec upstream '%s' returned unparseable world state sync status, defaulting to STATE=1", a.upstreamId)
		return 1
	}
	return int64(status.OldestHistoricBlockNumber)
}

type worldStateSyncStatus struct {
	OldestHistoricBlockNumber uint64 `json:"oldestHistoricBlockNumber"`
}

var _ LowerBoundDetector = (*AztecLowerBoundDetector)(nil)
