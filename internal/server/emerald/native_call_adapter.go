package emerald

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
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
		chunkSize uint32,
	) error
}

func adapterFor(item *dshackle.NativeCallItem) nativeCallAdapter {
	if item.GetRestData() != nil {
		return restNativeCallAdapter{}
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

type jsonRpcNativeCallAdapter struct{}

func (jsonRpcNativeCallAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	item *dshackle.NativeCallItem,
	requestSelector *dshackle.Selector,
	chunkSize uint32,
) (protocol.RequestHolder, *dshackle.NativeCallReplyItem) {
	payload := item.GetPayload()
	if len(payload) == 0 {
		payload = []byte("[]")
	}
	if !json.Valid(payload) {
		return nil, nativeCallErrorItem(
			item.GetId(),
			protocol.ClientError(fmt.Errorf("payload is not a valid JSON value")),
			flow.NoUpstream,
			nil,
			nil,
		)
	}

	requestID := strconv.FormatUint(uint64(item.GetId()), 10)
	body := protocol.JsonRpcRequestBody{Id: []byte(requestID), Method: item.GetMethod(), Params: payload}
	selectors, err := mapNativeCallSelectors(requestSelector, item.GetSelectors())
	if err != nil {
		return nil, nativeCallErrorItem(item.GetId(), protocol.ClientError(err), flow.NoUpstream, nil, nil)
	}
	if chunkSize > 0 {
		return protocol.WithSelectors(protocol.NewStreamUpstreamJsonRpcRequest(requestID, body, chain.MethodSpec), selectors), nil
	}
	return protocol.WithSelectors(protocol.NewUpstreamJsonRpcRequest(requestID, body, false, chain.MethodSpec), selectors), nil
}

func (jsonRpcNativeCallAdapter) SendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	chunkSize uint32,
) error {
	return sendReply(stream, wrapper, chunkSize, unwrapJsonRpcResultStream)
}

type restNativeCallAdapter struct{}

func (restNativeCallAdapter) BuildRequest(
	chain *chains.ConfiguredChain,
	item *dshackle.NativeCallItem,
	requestSelector *dshackle.Selector,
	chunkSize uint32,
) (protocol.RequestHolder, *dshackle.NativeCallReplyItem) {
	restData := item.GetRestData()
	if restData == nil {
		return nil, nativeCallErrorItem(
			item.GetId(),
			protocol.ClientError(fmt.Errorf("rest_data is missing")),
			flow.NoUpstream,
			nil,
			nil,
		)
	}

	if err := validateRestMethodTemplate(item.GetMethod()); err != nil {
		return nil, nativeCallErrorItem(
			item.GetId(),
			protocol.ClientError(err),
			flow.NoUpstream,
			nil,
			nil,
		)
	}

	// gRPC clients already deliver path/headers/query params pre-structured,
	// so we plumb them straight into RequestParams instead of recomputing
	// anything. item.GetMethod() is taken as authoritative for the canonical
	// method template; the HTTP connector will expand it at send time using
	// PathParams to fill in any "*" wildcards.
	requestParams := &protocol.RequestParams{
		PathParams:  append([]string(nil), restData.GetPathParams()...),
		Headers:     keyValueListToMap(restData.GetHeaders()),
		QueryParams: keyValueListToMap(restData.GetQueryParams()),
	}

	requestID := strconv.FormatUint(uint64(item.GetId()), 10)
	selectors, err := mapNativeCallSelectors(requestSelector, item.GetSelectors())
	if err != nil {
		return nil, nativeCallErrorItem(item.GetId(), protocol.ClientError(err), flow.NoUpstream, nil, nil)
	}
	if chunkSize > 0 {
		return protocol.WithSelectors(protocol.NewStreamUpstreamRestRequest(requestID, item.GetMethod(), requestParams, restData.GetPayload(), chain.MethodSpec), selectors), nil
	}
	return protocol.WithSelectors(protocol.NewUpstreamRestRequest(requestID, item.GetMethod(), requestParams, restData.GetPayload(), chain.MethodSpec), selectors), nil
}

func (restNativeCallAdapter) SendReply(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	chunkSize uint32,
) error {
	return sendReply(stream, wrapper, chunkSize, passThroughStream)
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
	chunkSize uint32,
	mode streamMode,
) error {
	if wrapper == nil || wrapper.Response == nil {
		return fmt.Errorf("response wrapper is empty")
	}
	var headers http.Header
	if resp, ok := wrapper.Response.(*protocol.BaseUpstreamResponse); ok {
		headers = resp.ResponseHeaders()
	}
	requestID := parseCallItemID(wrapper.RequestId)
	finalizationData := nativeCallFinalizationData(wrapper)

	if wrapper.Response.HasError() {
		replyItem := nativeCallErrorItem(requestID, wrapper.Response.GetError(), wrapper.UpstreamId, wrapper.Response.ResponseResult(), headers)
		replyItem.UpstreamNodeVersion = wrapper.UpstreamNodeVersion
		replyItem.Finalization = finalizationData
		return stream.Send(replyItem)
	}

	if wrapper.Response.HasStream() {
		reader := wrapper.Response.EncodeResponse([]byte("0"))
		if err := streamNativeCallBody(requestID, wrapper.UpstreamId, wrapper.UpstreamNodeVersion, finalizationData, reader, chunkSize, mode, headers, stream); err != nil {
			replyItem := nativeCallErrorItem(requestID, protocol.ServerErrorWithCause(err), wrapper.UpstreamId, nil, headers)
			replyItem.UpstreamNodeVersion = wrapper.UpstreamNodeVersion
			replyItem.Finalization = finalizationData
			return stream.Send(replyItem)
		}
		return nil
	}

	payload := append([]byte(nil), wrapper.Response.ResponseResult()...)
	for _, replyItem := range nativeCallSuccessItems(requestID, wrapper.UpstreamId, payload, chunkSize, headers) {
		replyItem.UpstreamNodeVersion = wrapper.UpstreamNodeVersion
		replyItem.Finalization = finalizationData
		if err := stream.Send(replyItem); err != nil {
			return err
		}
	}
	return nil
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
	requestID uint32,
	upstreamID string,
	upstreamNodeVersion string,
	finalization *dshackle.FinalizationData,
	reader io.Reader,
	chunkSize uint32,
	mode streamMode,
	header http.Header,
	stream dshackle.Blockchain_NativeCallServer,
) error {
	effectiveChunkSize := int(chunkSize)
	if effectiveChunkSize <= 0 {
		effectiveChunkSize = protocol.MaxChunkSize
	}
	emitter := newNativeCallChunkEmitter(effectiveChunkSize, func(chunk []byte, final bool) error {
		return stream.Send(&dshackle.NativeCallReplyItem{
			Id:                  requestID,
			Succeed:             true,
			Payload:             chunk,
			Chunked:             true,
			FinalChunk:          final,
			UpstreamId:          upstreamID,
			UpstreamNodeVersion: upstreamNodeVersion,
			Finalization:        finalization,
			ResponseHeaders:     mapHeaders(header),
		})
	})

	switch mode {
	case unwrapJsonRpcResultStream:
		if err := streamJsonRPCResult(reader, emitter); err != nil {
			return err
		}
	case passThroughStream:
		if _, err := io.Copy(emitter, reader); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown stream mode %d", mode)
	}
	return emitter.Flush()
}

// validateRestMethodTemplate checks that a gRPC-supplied method string is
// well-formed: "VERB#/path", both halves non-empty. The actual verb/path
// split happens inside the HTTP connector when the request is sent.
func validateRestMethodTemplate(method string) error {
	parts := strings.SplitN(method, protocol.MethodSeparator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("rest method must be in form VERB%spath, got %q", protocol.MethodSeparator, method)
	}
	return nil
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
