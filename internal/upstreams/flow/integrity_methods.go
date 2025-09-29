package flow

import (
	"context"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/rs/zerolog"
)

type IntegrityBlock interface {
	block()
}

type HeadBlock struct {
	number uint64
}

func NewHeadBlock(number uint64) *HeadBlock {
	return &HeadBlock{number: number}
}

func (h *HeadBlock) block() {
}

type FinalizedBlock struct {
	number uint64
}

func NewFinalizedBlock(number uint64) *FinalizedBlock {
	return &FinalizedBlock{number: number}
}

func (f *FinalizedBlock) block() {
}

type IntegrityHandler interface {
	CanBeProcessed(ctx context.Context, request protocol.RequestHolder) bool
	// HandleResponse - Processes a response from an upstream and determines if additional actions are required to support integrity. Returns:
	//
	// bool – indicates whether an additional request should be sent.
	//
	//[]string – if true, contains the list of upstream IDs where the additional request should be executed.
	//
	//IntegrityBlock – an object used to update chain state manually (e.g., replacing the tracked head block or finalization data) when necessary.
	HandleResponse(
		ctx context.Context,
		chainSupervisor *upstreams.ChainSupervisor,
		request protocol.RequestHolder,
		currentResponse *protocol.ResponseHolderWrapper,
	) (bool, []string, IntegrityBlock)
}

type NoopIntegrityHandler struct{}

func (n *NoopIntegrityHandler) CanBeProcessed(_ context.Context, _ protocol.RequestHolder) bool {
	return false
}

func (n *NoopIntegrityHandler) HandleResponse(
	_ context.Context,
	_ *upstreams.ChainSupervisor,
	_ protocol.RequestHolder,
	_ *protocol.ResponseHolderWrapper,
) (bool, []string, IntegrityBlock) {
	return false, nil, nil
}

func NewNoopIntegrityHandler() *NoopIntegrityHandler {
	return &NoopIntegrityHandler{}
}

var _ IntegrityHandler = (*NoopIntegrityHandler)(nil)

type EthBlockNumberIntegrityHandler struct {
}

func (e *EthBlockNumberIntegrityHandler) CanBeProcessed(_ context.Context, req protocol.RequestHolder) bool {
	return req.Method() == specs.EthBlockNumber
}

func (e *EthBlockNumberIntegrityHandler) HandleResponse(
	ctx context.Context,
	chainSupervisor *upstreams.ChainSupervisor,
	_ protocol.RequestHolder,
	currentResponse *protocol.ResponseHolderWrapper,
) (bool, []string, IntegrityBlock) {
	if currentResponse.Response.HasError() {
		return false, nil, nil
	}

	responseBlockNumber, err := hexutil.DecodeUint64(string(currentResponse.Response.ResponseResult()[1 : len(currentResponse.Response.ResponseResult())-1]))
	if err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msgf("couldn't decode blockNumber, response - %s", string(currentResponse.Response.ResponseResult()))
		return false, nil, nil
	}

	chainHead := chainSupervisor.GetChainState().HeadData.Head
	if responseBlockNumber >= chainHead {
		return false, nil, NewHeadBlock(responseBlockNumber)
	}

	filterF := func(upId string, state *protocol.UpstreamState) bool {
		return upId != currentResponse.UpstreamId && filterHeadFunc(responseBlockNumber, state)
	}
	return true, chainSupervisor.GetSortedUpstreamIds(filterF, sortHeadHeightFunc), nil
}

func NewEthBlockNumberIntegrityHandler() *EthBlockNumberIntegrityHandler {
	return &EthBlockNumberIntegrityHandler{}
}

var _ IntegrityHandler = (*EthBlockNumberIntegrityHandler)(nil)

type EthGetBlockByNumberIntegrityHandler struct {
}

func (e *EthGetBlockByNumberIntegrityHandler) CanBeProcessed(_ context.Context, request protocol.RequestHolder) bool {
	return request.Method() == specs.EthGetBlockByNumber
}

func (e *EthGetBlockByNumberIntegrityHandler) HandleResponse(
	ctx context.Context,
	chainSupervisor *upstreams.ChainSupervisor,
	request protocol.RequestHolder,
	currentResponse *protocol.ResponseHolderWrapper,
) (bool, []string, IntegrityBlock) {
	if currentResponse.Response.HasError() {
		return false, nil, nil
	}

	logger := zerolog.Ctx(ctx)
	numberNode, err := sonic.Get(currentResponse.Response.ResponseResult(), "number")
	if err != nil {
		logger.Err(err).Msgf("couldn't parse the 'number' field, response - %s", string(currentResponse.Response.ResponseResult()))
		return false, nil, nil
	}
	if numberNode.TypeSafe() != ast.V_STRING {
		logger.Err(err).Msg("couldn't get a string value of the 'number' field")
		return false, nil, nil
	}
	numberNodeStr, _ := numberNode.String()
	responseBlockNumber, err := hexutil.DecodeUint64(numberNodeStr)
	if err != nil {
		logger.Err(err).Msgf("couldn't decode the 'number' field, value - %s", numberNodeStr)
		return false, nil, nil
	}

	chainHead := chainSupervisor.GetChainState().HeadData.Head
	methodParam := request.ParseParams(ctx)
	blockNumberParam, _ := methodParam.(*specs.BlockNumberParam)
	if blockNumberParam != nil {
		switch blockNumberParam.BlockNumber {
		case rpc.LatestBlockNumber:
			if responseBlockNumber >= chainHead {
				return false, nil, NewHeadBlock(responseBlockNumber)
			}
			filterF := func(upId string, state *protocol.UpstreamState) bool {
				return upId != currentResponse.UpstreamId && filterHeadFunc(responseBlockNumber, state)
			}
			return true, chainSupervisor.GetSortedUpstreamIds(filterF, sortHeadHeightFunc), nil
		case rpc.FinalizedBlockNumber:
			finalization, ok := chainSupervisor.GetChainState().Blocks[protocol.FinalizedBlock]
			if !ok || responseBlockNumber >= finalization.Height {
				return false, nil, NewFinalizedBlock(responseBlockNumber)
			}
			filterF := func(upId string, state *protocol.UpstreamState) bool {
				return upId != currentResponse.UpstreamId && filterFinalizationFunc(responseBlockNumber, state)
			}
			return true, chainSupervisor.GetSortedUpstreamIds(filterF, sortFinalizationHeightFunc), nil
		default:
			if responseBlockNumber >= chainHead {
				return false, nil, NewHeadBlock(responseBlockNumber)
			}
		}
	}
	return false, nil, nil
}

func NewEthGetBlockByNumberIntegrityHandler() *EthGetBlockByNumberIntegrityHandler {
	return &EthGetBlockByNumberIntegrityHandler{}
}

var _ IntegrityHandler = (*EthGetBlockByNumberIntegrityHandler)(nil)
