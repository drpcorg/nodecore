package flow

import (
	"context"
	"errors"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/rs/zerolog"
)

// NotNullRequestProcessor handles methods where a successful JSON-RPC null can
// be caused by indexing lag on a single upstream rather than by a real network-wide
// miss. It tries candidate upstreams sequentially and stops on the first successful
// non-null response. If every upstream returns null, it returns the first null result;
// otherwise it falls back to the first upstream error response or the last execution
// error.
type NotNullRequestProcessor struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func NewNotNullRequestProcessor(upstreamSupervisor upstreams.UpstreamSupervisor) *NotNullRequestProcessor {
	return &NotNullRequestProcessor{upstreamSupervisor: upstreamSupervisor}
}

func (p *NotNullRequestProcessor) ProcessRequest(
	ctx context.Context,
	upstreamStrategy UpstreamStrategy,
	request protocol.RequestHolder,
) ProcessedResponse {
	upstreamIDs, err := collectDispatchUpstreamIDs(upstreamStrategy, request)
	if err != nil {
		return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, err)}
	}

	var nullResult *protocol.ResponseHolderWrapper
	var fallback *protocol.ResponseHolderWrapper
	var lastErr error

	for _, upstreamID := range upstreamIDs {
		select {
		case <-ctx.Done():
			return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, ctx.Err())}
		default:
		}

		upstream := p.upstreamSupervisor.GetUpstream(upstreamID)
		if upstream == nil {
			lastErr = protocol.NoAvailableUpstreamsError()
			continue
		}

		wrapper, err := sendUnaryRequest(ctx, upstream, request)
		if err != nil {
			lastErr = err
			continue
		}
		if wrapper == nil || wrapper.Response == nil {
			lastErr = errors.New("not-null upstream returned empty response")
			continue
		}
		if wrapper.Response.HasStream() {
			zerolog.Ctx(ctx).Debug().Msgf("not-null dispatch selected streaming upstream %s for method %s", wrapper.UpstreamId, request.Method())
			return &UnaryResponse{ResponseWrapper: wrapper}
		}
		if wrapper.Response.HasError() {
			if fallback == nil {
				fallback = wrapper
			}
			continue
		}
		if isNullResponse(wrapper.Response) {
			if nullResult == nil {
				nullResult = wrapper
			}
			continue
		}

		zerolog.Ctx(ctx).Debug().Msgf("not-null dispatch selected upstream %s for method %s", wrapper.UpstreamId, request.Method())
		return &UnaryResponse{ResponseWrapper: wrapper}
	}

	if nullResult != nil {
		return &UnaryResponse{ResponseWrapper: nullResult}
	}
	if fallback != nil {
		return &UnaryResponse{ResponseWrapper: fallback}
	}
	if lastErr != nil {
		return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, lastErr)}
	}
	return &UnaryResponse{ResponseWrapper: totalFailureWrapper(request, protocol.NoAvailableUpstreamsError())}
}

var _ RequestProcessor = (*NotNullRequestProcessor)(nil)

func isNullResponse(response protocol.ResponseHolder) bool {
	return strings.TrimSpace(string(response.ResponseResult())) == "null"
}
