package flow

import (
	"context"
	"github.com/drpcorg/dsheltie/internal/caches"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/internal/upstreams"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"sync"
)

type ExecutionFlow interface {
	Execute(ctx context.Context, requests []protocol.RequestHolder)
	GetResponses() chan *protocol.ResponseHolderWrapper
}

type BaseExecutionFlow struct {
	chain              chains.Chain
	upstreamSupervisor upstreams.UpstreamSupervisor
	wg                 sync.WaitGroup
	responseChan       chan *protocol.ResponseHolderWrapper
	cacheProcessor     caches.CacheProcessor
	subCtx             *SubCtx
}

func NewBaseExecutionFlow(
	chain chains.Chain,
	upstreamSupervisor upstreams.UpstreamSupervisor,
	cacheProcessor caches.CacheProcessor,
	subCtx *SubCtx,
) *BaseExecutionFlow {
	return &BaseExecutionFlow{
		chain:              chain,
		cacheProcessor:     cacheProcessor,
		upstreamSupervisor: upstreamSupervisor,
		responseChan:       make(chan *protocol.ResponseHolderWrapper),
		subCtx:             subCtx,
	}
}

func (e *BaseExecutionFlow) GetResponses() chan *protocol.ResponseHolderWrapper {
	return e.responseChan
}

func (e *BaseExecutionFlow) Execute(ctx context.Context, requests []protocol.RequestHolder) {
	defer close(e.responseChan)
	e.wg.Add(len(requests))

	for _, request := range requests {
		upstreamStrategy := NewBaseStrategy(e.upstreamSupervisor.GetChainSupervisor(e.chain))
		e.processRequest(ctx, upstreamStrategy, request)
	}

	e.wg.Wait()
}

func (e *BaseExecutionFlow) processRequest(ctx context.Context, upstreamStrategy UpstreamStrategy, request protocol.RequestHolder) {
	go func() {
		execCtx := context.WithValue(ctx, upstreams.RequestKey, request)
		var requestProcessor RequestProcessor

		if request.IsSubscribe() {
			requestProcessor = NewSubscriptionRequestProcessor(e.upstreamSupervisor, e.subCtx)
		} else if isLocalRequest(e.chain, request.Method()) {
			requestProcessor = NewLocalRequestProcessor(e.chain, e.subCtx)
		} else {
			requestProcessor = NewUnaryRequestProcessor(e.chain, e.cacheProcessor, e.upstreamSupervisor)
		}

		processedResponse := requestProcessor.ProcessRequest(execCtx, upstreamStrategy, request)

		switch resp := processedResponse.(type) {
		case *UnaryResponse:
			e.responseChan <- resp.ResponseWrapper
		case *SubscriptionResponse:
			for wrapper := range resp.ResponseWrappers {
				e.responseChan <- wrapper
			}
		}

		e.wg.Done()
	}()
}

func isLocalRequest(chain chains.Chain, method string) bool {
	return specs.IsUnsubscribeMethod(chains.GetMethodSpecNameByChain(chain), method)
}

type SubCtx struct {
	subscriptions utils.CMap[string, context.CancelFunc]
}

func NewSubCtx() *SubCtx {
	return &SubCtx{
		subscriptions: utils.CMap[string, context.CancelFunc]{},
	}
}

func (s *SubCtx) AddSub(sub string, cancel context.CancelFunc) {
	s.subscriptions.Store(sub, &cancel)
}

func (s *SubCtx) Unsubscribe(sub string) {
	cancel, ok := s.subscriptions.Load(sub)
	if ok {
		s.subscriptions.Delete(sub)
		(*cancel)()
	}
}

func (s *SubCtx) Exists(sub string) bool {
	_, ok := s.subscriptions.Load(sub)
	return ok
}
