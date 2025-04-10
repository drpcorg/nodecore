package flow

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams"
	"github.com/drpcorg/dshaltie/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"sync"
)

type ExecutionFlow interface {
	Execute(ctx context.Context, requests []protocol.RequestHolder)
	GetResponses() chan *protocol.ResponseHolderWrapper
}

type SingleRequestExecutionFlow struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	responsesInternal  chan *protocol.ResponseHolderWrapper
	wg                 sync.WaitGroup
	responseChan       chan *protocol.ResponseHolderWrapper
}

func NewSingleRequestExecutionFlow(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
) *SingleRequestExecutionFlow {
	return &SingleRequestExecutionFlow{
		chain:              chain,
		upstreamSupervisor: upstreamSupervisor,
		responsesInternal:  make(chan *protocol.ResponseHolderWrapper),
		responseChan:       make(chan *protocol.ResponseHolderWrapper),
	}
}

func (e *SingleRequestExecutionFlow) GetResponses() chan *protocol.ResponseHolderWrapper {
	return e.responseChan
}

func (e *SingleRequestExecutionFlow) Execute(ctx context.Context, requests []protocol.RequestHolder) {
	defer close(e.responseChan)
	e.wg.Add(len(requests))
	go func() {
		e.wg.Wait()
		close(e.responsesInternal)
	}()

	for _, request := range requests {
		upstreamStrategy := NewBaseStrategy(e.upstreamSupervisor.GetChainSupervisor(e.chain))
		e.processRequest(ctx, upstreamStrategy, request)
	}

	for response := range e.responsesInternal {
		e.responseChan <- response
	}
}

func (e *SingleRequestExecutionFlow) processRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) {
	go func() {
		execCtx := context.WithValue(ctx, upstreams.RequestKey, request)
		response, err := e.upstreamSupervisor.
			GetExecutor().
			WithContext(execCtx).
			GetWithExecution(func(exec failsafe.Execution[*protocol.ResponseHolderWrapper]) (*protocol.ResponseHolderWrapper, error) {
				upstreamId, err := upstreamStrategy.SelectUpstream(request)
				if err != nil {
					return nil, upstreams.ExecutionError(exec.Hedges(), err)
				}
				result, err := sendRequest(ctx, e.upstreamSupervisor.GetUpstream(upstreamId), request)
				if err != nil {
					return nil, upstreams.ExecutionError(exec.Hedges(), err)
				}
				return result, nil
			})
		if err != nil {
			response = &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewReplyErrorFromErr(request.Id(), err, request.RequestType()),
			}
		}

		e.responsesInternal <- response
		e.wg.Done()
	}()
}

func sendRequest(
	ctx context.Context,
	upstream *upstreams.Upstream,
	request protocol.RequestHolder,
) (*protocol.ResponseHolderWrapper, error) {
	var requestProcessor UpstreamRequestProcessor
	var err error

	switch request.(type) {
	case *protocol.HttpUpstreamRequest:
		requestProcessor, err = NewHttpUpstreamRequestProcessor(upstream, protocol.JsonRpcConnector)
	}
	if err != nil {
		return nil, err
	}
	response := requestProcessor.Execute(ctx, request)

	return response, nil
}
