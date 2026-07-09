package solana_bounds

import (
	"context"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/rs/zerolog/log"
)

const period = 3 * time.Minute

type SolanaLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	internalTimeout time.Duration

	executor failsafe.Executor[solanaLowerBound]
}

func (s *SolanaLowerBoundDetector) DetectLowerBound(ctx context.Context) ([]protocol.LowerBoundData, error) {
	result, err := s.executor.WithContext(ctx).GetWithExecution(func(exec failsafe.Execution[solanaLowerBound]) (solanaLowerBound, error) {
		var zero solanaLowerBound
		slot, err := s.getFirstAvailableBlock(ctx)
		if err != nil {
			return zero, err
		}

		block, err := s.getBlock(ctx, slot)
		if err != nil {
			return zero, err
		}
		return newSolanaLowerBound(slot, block), nil
	})
	if err != nil {
		return nil, err
	}

	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(result.slot, protocol.SlotBound),
		protocol.NewLowerBoundDataNow(result.block, protocol.StateBound),
	}, nil
}

func (s *SolanaLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.SlotBound, protocol.StateBound}
}

func (s *SolanaLowerBoundDetector) Period() time.Duration {
	return period
}

func (s *SolanaLowerBoundDetector) getFirstAvailableBlock(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getFirstAvailableBlock", nil, chains.SOLANA)
	if err != nil {
		return 0, err
	}

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	number, err := strconv.Atoi(string(response.ResponseResult()))
	if err != nil {
		return 0, err
	}
	return int64(max(number, 1)), nil
}

func (s *SolanaLowerBoundDetector) getBlock(ctx context.Context, number int64) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, s.internalTimeout)
	defer cancel()

	params := map[string]any{
		"showRewards":                    false,
		"transactionDetails":             "none",
		"maxSupportedTransactionVersion": 0,
	}
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getBlock", []any{number, params}, chains.SOLANA)
	if err != nil {
		return 0, err
	}

	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}

	solanaBlock := SolanaBlock{}
	err = sonic.Unmarshal(response.ResponseResult(), &solanaBlock)
	if err != nil {
		return 0, err
	}

	return max(solanaBlock.BlockHeight, 1), nil
}

func NewSolanaLowerBoundDetector(upstreamId string, internalTimeout time.Duration, connector connectors.ApiConnector) *SolanaLowerBoundDetector {
	return &SolanaLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		internalTimeout: internalTimeout,
		executor:        failsafe.NewExecutor(createDetectionRetryPolicy(upstreamId)),
	}
}

func createDetectionRetryPolicy(upstreamId string) failsafe.Policy[solanaLowerBound] {
	retryPolicy := retrypolicy.Builder[solanaLowerBound]()

	retryPolicy.WithMaxAttempts(20)
	retryPolicy.WithBackoff(1*time.Second, 60*time.Second)
	retryPolicy.WithJitter(3 * time.Second)

	retryPolicy.HandleIf(func(result solanaLowerBound, err error) bool {
		return err != nil
	})

	retryPolicy.OnRetry(func(event failsafe.ExecutionEvent[solanaLowerBound]) {
		err := event.LastError()
		log.Debug().Err(err).Msgf("couldn't calculate slot+state solana lower bound of upstream '%s'", upstreamId)
	})

	return retryPolicy.Build()
}

var _ lower_bounds.LowerBoundDetector = (*SolanaLowerBoundDetector)(nil)

type SolanaBlock struct {
	BlockHeight int64 `json:"blockHeight"`
}

type solanaLowerBound struct {
	slot  int64
	block int64
}

func newSolanaLowerBound(slot, block int64) solanaLowerBound {
	return solanaLowerBound{
		slot:  slot,
		block: block,
	}
}
