package flow

import (
	"context"

	"github.com/drpcorg/nodecore/internal/caches"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/quorum"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// CacheRequestProcessor decorates another RequestProcessor with caching: it does
// a cache Receive first and only delegates to the inner processor on a miss, then
// stores a cacheable response. A cache hit short-circuits the inner processor
// entirely, so expensive delegates (e.g. NotNullRequestProcessor walking upstreams)
// don't run when the answer is already cached.
type CacheRequestProcessor struct {
	chain          chains.Chain
	cacheProcessor caches.CacheProcessor
	delegate       RequestProcessor
}

func NewCacheRequestProcessor(chain chains.Chain, cacheProcessor caches.CacheProcessor, delegate RequestProcessor) *CacheRequestProcessor {
	return &CacheRequestProcessor{
		chain:          chain,
		cacheProcessor: cacheProcessor,
		delegate:       delegate,
	}
}

var _ RequestProcessor = (*CacheRequestProcessor)(nil)

func (p *CacheRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	// Quorum-read requests are served fresh from drpc upstreams: cached payloads
	// don't carry the QR* signature headers, so both cache Receive and Store
	// are skipped here.
	if _, quorumRequested := quorum.FromContext(ctx); quorumRequested {
		return p.delegate.ProcessRequest(ctx, upstreamStrategy, request)
	}

	if result, ok := p.cacheProcessor.Receive(ctx, p.chain, request); ok {
		// change the previous request type since it will not be sent to the upstream
		request.RequestObserver().
			WithRequestKind(protocol.Cached)
		return &UnaryResponse{
			ResponseWrapper: &protocol.ResponseHolderWrapper{
				UpstreamId: NoUpstream,
				RequestId:  request.Id(),
				Response:   protocol.NewSimpleHttpUpstreamResponse(request.Id(), result, request.RequestType()),
			},
		}
	}

	processedResponse := p.delegate.ProcessRequest(ctx, upstreamStrategy, request)

	if unaryResponse, ok := processedResponse.(*UnaryResponse); ok {
		response := unaryResponse.ResponseWrapper.Response
		if !response.HasError() && !response.HasStream() {
			go p.cacheProcessor.Store(ctx, p.chain, request, response.ResponseResult())
		}
	}

	return processedResponse
}
