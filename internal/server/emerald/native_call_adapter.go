package emerald

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/rs/zerolog/log"
)

// nativeCallAdapter bridges a single NativeCallItem to the internal protocol
// layer and back: it turns the protobuf request into a protocol.RequestHolder
// of the right API kind, and turns the resulting ResponseHolderWrapper into
// NativeCallReplyItem(s) on the wire.

type nativeCallAdapter interface {
	BuildRequest(
		chain *chains.ConfiguredChain,
		item *dshackle.NativeCallItem,
		requestSelector *dshackle.Selector,
		chunkSize uint32,
	) (protocol.RequestHolder, *dshackle.NativeCallReplyItem)

	SendReply(
		stream dshackle.Blockchain_NativeCallServer,
		wrapper *protocol.ResponseHolderWrapper,
		nonce uint64,
		signer signature.ResponseSigner,
	) error

	// ErrorItem renders a failure that happened before or outside the flow
	// (no upstream) in the item's own error vocabulary.
	ErrorItem(requestID uint32, responseError *protocol.ResponseError) *dshackle.NativeCallReplyItem
}

// errorItemRenderer builds the error reply item in the vocabulary of the item's
// API kind: JSON-RPC and REST items carry nodecore error codes and error_data,
// gRPC items carry a canonical gRPC status. Response-level metadata is not the
// renderer's business - sendReply stamps it via replyMeta.
type errorItemRenderer func(requestID uint32, responseError *protocol.ResponseError, errorAsIs []byte) *dshackle.NativeCallReplyItem

func adapterFor(item *dshackle.NativeCallItem) nativeCallAdapter {
	if item.GetRestData() != nil {
		return restNativeCallAdapter{}
	}
	if item.GetGrpcData() != nil {
		return grpcNativeCallAdapter{}
	}
	return jsonRpcNativeCallAdapter{}
}

func mapNativeCallSelectors(requestSelector *dshackle.Selector, itemSelectors []*dshackle.Selector) ([]protocol.RequestSelector, error) {
	selectors := make([]protocol.RequestSelector, 0, 1+len(itemSelectors))
	if requestSelector != nil {
		selectors = append(selectors, mapDshackleSelector(requestSelector))
	}
	selectors = append(selectors, mapDshackleSelectors(itemSelectors)...)
	return rejectConflictingSortSelectors(selectors)
}

type streamMode int

const (
	// unwrapJsonRpcResultStream parses a JSON-RPC envelope on the fly and emits
	// only the bytes of the `result` field.
	unwrapJsonRpcResultStream streamMode = iota
	// passThroughStream emits the upstream body verbatim.
	passThroughStream
)

func sendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	nonce uint64,
	signer signature.ResponseSigner,
	mode streamMode,
	renderError errorItemRenderer,
) error {
	if wrapper == nil || wrapper.Response == nil {
		return fmt.Errorf("response wrapper is empty")
	}
	meta := newReplyMeta(wrapper)
	response := wrapper.Response

	if response.HasError() {
		return stream.Send(meta.stamp(renderError(meta.requestID, response.GetError(), response.ResponseResult())))
	}

	if response.HasStream() {
		// a missing hint must fail the unwrap, not read as "result at offset 0"
		hint := protocol.NoJsonRpcResultStreamHint
		if h, ok := response.GetStreamHint().(protocol.JsonRpcResultStreamHint); ok {
			hint = h
		}
		if err := streamNativeCallBody(stream, response.EncodeResponse([]byte("0")), mode, hint, meta); err != nil {
			return stream.Send(meta.stamp(renderError(meta.requestID, protocol.ServerErrorWithCause(err), nil)))
		}
		return nil
	}

	payload := append([]byte(nil), response.ResponseResult()...)
	replySignature, err := buildReplySignature(signer, nonce, payload, wrapper.UpstreamId)
	if err != nil {
		log.Warn().Err(err).Msgf("unable to sign a response of request %s", wrapper.RequestId)
		// ErrSigningNotConfigured is meant for the client and is normally caught
		// before dispatch; any other cause is an internal detail, so it is logged
		// rather than put on the wire.
		responseError := protocol.ServerError()
		if errors.Is(err, signature.ErrSigningNotConfigured) {
			responseError = protocol.ServerErrorWithCause(err)
		}
		return stream.Send(meta.stamp(renderError(meta.requestID, responseError, nil)))
	}

	replyItem := meta.stamp(nativeCallSuccessItem(meta.requestID, payload))
	replyItem.Signature = replySignature
	return stream.Send(replyItem)
}

// replyMeta is the response-level metadata every reply item of one request
// carries: who served it and what the upstream said around the body. It is
// stamped on a buffered item, or on the first chunk of a streamed one.
type replyMeta struct {
	requestID           uint32
	upstreamID          string
	upstreamNodeVersion string
	finalization        *dshackle.FinalizationData
	headers             []*dshackle.KeyValue
	trailers            []*dshackle.KeyValue
}

func newReplyMeta(wrapper *protocol.ResponseHolderWrapper) replyMeta {
	headers, trailers := responseMetadata(wrapper.Response)
	return replyMeta{
		requestID:           parseCallItemID(wrapper.RequestId),
		upstreamID:          wrapper.UpstreamId,
		upstreamNodeVersion: wrapper.UpstreamNodeVersion,
		finalization:        nativeCallFinalizationData(wrapper),
		headers:             mapHeaders(headers),
		trailers:            mapHeaders(trailers),
	}
}

func (m replyMeta) stamp(item *dshackle.NativeCallReplyItem) *dshackle.NativeCallReplyItem {
	item.UpstreamId = m.upstreamID
	item.UpstreamNodeVersion = m.upstreamNodeVersion
	item.Finalization = m.finalization
	item.ResponseHeaders = m.headers
	item.ResponseTrailers = m.trailers
	return item
}

// responseMetadata reads the upstream's response metadata through the optional
// capabilities, so success responses and error replies (a RESOURCE_EXHAUSTED
// with rate-limit hints in its trailers is a *ReplyError) are treated alike.
func responseMetadata(response protocol.ResponseHolder) (http.Header, map[string][]string) {
	var headers http.Header
	var trailers map[string][]string
	if headerBearer, ok := response.(protocol.HasResponseHeaders); ok {
		headers = headerBearer.ResponseHeaders()
	}
	if trailerBearer, ok := response.(protocol.HasResponseTrailers); ok {
		trailers = trailerBearer.ResponseTrailers()
	}
	return headers, trailers
}

func nativeCallFinalizationData(wrapper *protocol.ResponseHolderWrapper) *dshackle.FinalizationData {
	if wrapper == nil || wrapper.FinalizationBlockType == nil {
		return nil
	}

	finalizationType := dshackle.FinalizationType_FINALIZATION_SAFE_BLOCK
	if *wrapper.FinalizationBlockType == protocol.FinalizedBlock {
		finalizationType = dshackle.FinalizationType_FINALIZATION_FINALIZED_BLOCK
	}

	return &dshackle.FinalizationData{Height: wrapper.FinalizationBlock.Height, Type: finalizationType}
}

func streamNativeCallBody(
	stream dshackle.Blockchain_NativeCallServer,
	reader io.Reader,
	mode streamMode,
	hint protocol.JsonRpcResultStreamHint,
	meta replyMeta,
) error {
	emitter := newNativeCallChunkEmitter(func(chunk []byte, first, final bool) error {
		item := &dshackle.NativeCallReplyItem{
			Id:         meta.requestID,
			Succeed:    true,
			Payload:    chunk,
			Chunked:    true,
			FinalChunk: final,
		}
		// Response-level metadata travels on the first chunk only.
		if first {
			meta.stamp(item)
		}
		return stream.Send(item)
	})

	ctx := stream.Context()
	switch mode {
	case unwrapJsonRpcResultStream:
		if err := streamJsonRPCResult(ctx, reader, emitter, hint.ResultStart, hint.Counter); err != nil {
			return err
		}
	case passThroughStream:
		// Read-ahead so the upstream read overlaps the cross-network stream.Send.
		// The callback never reports done; the loop runs until EOF and Finish
		// sends the terminal final chunk.
		if err := streamReadAhead(ctx, reader, func(buf []byte) (bool, error) {
			return false, emitter.WriteChunk(buf, false)
		}); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown stream mode %d", mode)
	}
	return emitter.Finish()
}

// keyValueListToMap collapses a dshackle KeyValue repeated field into the
// multi-valued map shape used by protocol.RequestParams. Returns nil for
// empty input so callers don't see an empty allocation in RequestParams.
func keyValueListToMap(items []*dshackle.KeyValue) map[string][]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string][]string, len(items))
	for _, kv := range items {
		out[kv.GetKey()] = append(out[kv.GetKey()], kv.GetValue())
	}
	return out
}
