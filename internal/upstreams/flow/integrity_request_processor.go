package flow

import (
	"cmp"
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type IntegrityRequestProcessor struct {
	requestProcessor   RequestProcessor
	upstreamSupervisor upstreams.UpstreamSupervisor
	chain              chains.Chain
}

var sortHeadHeightFunc = func(entry1 utils.Pair[string, *protocol.UpstreamState], entry2 utils.Pair[string, *protocol.UpstreamState]) int {
	return cmp.Compare(entry2.S.HeadData.Height, entry1.S.HeadData.Height)
}

var filterHeadFunc = func(currentHead uint64, state *protocol.UpstreamState) bool {
	return state.HeadData != nil && state.HeadData.Height > currentHead
}

var sortFinalizationHeightFunc = func(entry1 utils.Pair[string, *protocol.UpstreamState], entry2 utils.Pair[string, *protocol.UpstreamState]) int {
	return cmp.Compare(entry2.S.BlockInfo.GetBlock(protocol.FinalizedBlock).Height, entry1.S.BlockInfo.GetBlock(protocol.FinalizedBlock).Height)
}

var filterFinalizationFunc = func(currentFinalization uint64, state *protocol.UpstreamState) bool {
	return state.BlockInfo != nil && state.BlockInfo.GetBlock(protocol.FinalizedBlock) != nil && state.BlockInfo.GetBlock(protocol.FinalizedBlock).Height > currentFinalization
}

func (i *IntegrityRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	var integrityHandler IntegrityHandler
	switch request.Method() {
	case specs.EthBlockNumber:
		integrityHandler = NewEthBlockNumberIntegrityHandler()
	case specs.EthGetBlockByNumber:
		integrityHandler = NewEthGetBlockByNumberIntegrityHandler()
	default:
		integrityHandler = NewNoopIntegrityHandler()
	}

	if !integrityHandler.CanBeProcessed(ctx, request) {
		return i.requestProcessor.ProcessRequest(ctx, upstreamStrategy, request)
	}

	var response *protocol.ResponseHolderWrapper
	var err error

	response, err = executeUnaryRequest(ctx, i.chain, request, i.upstreamSupervisor, upstreamStrategy)
	if err != nil {
		response = &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewTotalFailureFromErr(request.Id(), err, request.RequestType()),
		}
	} else {
		if !response.Response.HasError() {
			response = i.handleResponse(ctx, integrityHandler, request, response)
		}
	}

	return &UnaryResponse{ResponseWrapper: response}
}

func NewIntegrityRequestProcessor(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	requestProcessor RequestProcessor,
) *IntegrityRequestProcessor {
	return &IntegrityRequestProcessor{
		requestProcessor:   requestProcessor,
		chain:              chain,
		upstreamSupervisor: upstreamSupervisor,
	}
}

var _ RequestProcessor = (*IntegrityRequestProcessor)(nil)

func (i *IntegrityRequestProcessor) handleResponse(
	ctx context.Context,
	handler IntegrityHandler,
	request protocol.RequestHolder,
	response *protocol.ResponseHolderWrapper,
) *protocol.ResponseHolderWrapper {
	chainSupervisor := i.upstreamSupervisor.GetChainSupervisor(i.chain)
	if chainSupervisor == nil {
		return response
	}
	shouldSendRequest, sortedUpstreams, integrityBlock := handler.HandleResponse(ctx, chainSupervisor, request, response)
	if integrityBlock != nil {
		i.updateBlocks(response.UpstreamId, integrityBlock)
	}
	if !shouldSendRequest || len(sortedUpstreams) == 0 {
		return response
	}

	upstreamStrategy := NewSpecificOrderUpstreamStrategy(sortedUpstreams, chainSupervisor)
	newResponse, err := executeUnaryRequest(ctx, i.chain, request, i.upstreamSupervisor, upstreamStrategy)
	if err != nil {
		return response
	}
	_, _, integrityBlock = handler.HandleResponse(ctx, chainSupervisor, request, newResponse)
	if integrityBlock != nil {
		i.updateBlocks(newResponse.UpstreamId, integrityBlock)
	}
	return newResponse
}

func (i *IntegrityRequestProcessor) updateBlocks(upstreamId string, integrityBlock IntegrityBlock) {
	responseUpstream := i.upstreamSupervisor.GetUpstream(upstreamId)
	if responseUpstream != nil {
		switch block := integrityBlock.(type) {
		case *HeadBlock:
			responseUpstream.UpdateHead(block.number, 0)
		case *FinalizedBlock:
			responseUpstream.UpdateBlock(protocol.NewBlockDataWithHeight(block.number), protocol.FinalizedBlock)
		}
	}
}
