package lower_bounds

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const aztecPeriod = 5 * time.Minute

var errAztecNoOldestHistoric = errors.New("aztec node returned no oldestHistoricBlockNumber")

// AztecLowerBoundDetector reports the lowest L2 block for which the upstream
// still has state available, by reading oldestHistoricBlockNumber from
// node_getWorldStateSyncStatus (one RPC per refresh - the world-state
// synchronizer publishes this number directly, no binary-search probing
// needed).
//
// On any error the detector returns (nil, err). BaseLowerBoundProcessor
// logs the error and skips the tick, so the previously cached lower bound
// stays in place. Faking a default value here would risk clobbering a real
// prune boundary on a transient endpoint outage (the public Aztec endpoint
// occasionally returns code 19 on this method per the Aztec team's audit).
type AztecLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewAztecLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AztecLowerBoundDetector {
	return &AztecLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AztecLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	bound, err := a.fetchOldestHistoric()
	if err != nil {
		return nil, err
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

func (a *AztecLowerBoundDetector) fetchOldestHistoric() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"node_getWorldStateSyncStatus",
		[]interface{}{},
		a.chain,
	)
	if err != nil {
		return 0, err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}

	var status worldStateSyncStatus
	if err := sonic.Unmarshal(response.ResponseResult(), &status); err != nil {
		return 0, fmt.Errorf("aztec world state sync status unparseable: %w", err)
	}
	if status.OldestHistoricBlockNumber == 0 {
		return 0, errAztecNoOldestHistoric
	}
	return int64(status.OldestHistoricBlockNumber), nil
}

type worldStateSyncStatus struct {
	OldestHistoricBlockNumber uint64 `json:"oldestHistoricBlockNumber"`
}

var _ LowerBoundDetector = (*AztecLowerBoundDetector)(nil)
