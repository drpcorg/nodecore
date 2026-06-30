package emerald

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	_ "google.golang.org/grpc/encoding/gzip"
)

const defaultNativeSubscribeHeartbeat = 30 * time.Second

var errSubscribeMappingNotSupported = errors.New("unsupported subscribe method mapping")

type GrpcBlockchainService struct {
	dshackle.UnimplementedBlockchainServer

	appCtx            *server_ctx.ApplicationServerContext
	sessionAuth       *grpcSessionAuth
	heartbeatInterval time.Duration
}

func NewGrpcBlockchainService(appCtx *server_ctx.ApplicationServerContext, sessionAuth *grpcSessionAuth) *GrpcBlockchainService {
	return &GrpcBlockchainService{
		appCtx:            appCtx,
		sessionAuth:       sessionAuth,
		heartbeatInterval: defaultNativeSubscribeHeartbeat,
	}
}

func (s *GrpcBlockchainService) SubscribeChainStatus(request *dshackle.SubscribeChainStatusRequest, stream dshackle.Blockchain_SubscribeChainStatusServer) error {
	if err := s.sessionAuth.requireSession(stream.Context()); err != nil {
		return err
	}
	if request == nil {
		return status.Error(codes.Internal, "request is nil")
	}
	if s.appCtx == nil || s.appCtx.UpstreamSupervisor == nil {
		return status.Error(codes.Unavailable, "upstream supervisor is not configured")
	}

	return SubscribeChainStatus(s.appCtx.UpstreamSupervisor, stream)
}

func (s *GrpcBlockchainService) NativeCall(request *dshackle.NativeCallRequest, stream dshackle.Blockchain_NativeCallServer) error {
	if err := s.sessionAuth.requireSession(stream.Context()); err != nil {
		return err
	}
	if request == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.ClientError(fmt.Errorf("request is nil")), flow.NoUpstream, nil, nil))
	}
	if s.appCtx == nil || s.appCtx.UpstreamSupervisor == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.NoAvailableUpstreamsError(), flow.NoUpstream, nil, nil))
	}

	configuredChain, chainSupervisor := s.resolveChain(request.GetChain())
	if configuredChain == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.WrongChainError(strconv.Itoa(int(request.GetChain()))), flow.NoUpstream, nil, nil))
	}
	if chainSupervisor == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.NoAvailableUpstreamsError(), flow.NoUpstream, nil, nil))
	}

	requests, adapters, preResponses := s.buildNativeCallRequests(configuredChain, request)
	for _, preResponse := range preResponses {
		if err := stream.Send(preResponse); err != nil {
			return err
		}
	}
	if len(requests) == 0 {
		return nil
	}

	executionFlow := flow.NewBaseExecutionFlow(
		configuredChain.Chain,
		s.appCtx.UpstreamSupervisor,
		s.appCtx.CacheProcessor,
		s.appCtx.Registry,
		s.appCtx.AppConfig,
		flow.NewSubCtx(),
		s.appCtx.QuorumRegistry,
		s.appCtx.SubEngineRegistry,
	)
	executionFlow.AddHooks(
		flow.NewMethodBanHook(s.appCtx.UpstreamSupervisor),
		dimensions.NewDimensionHook(s.appCtx.DimensionTracker),
	)

	go executionFlow.Execute(stream.Context(), requests)

	for wrapper := range executionFlow.GetResponses() {
		adapter, ok := adapters[wrapper.RequestId]
		if !ok {
			adapter = jsonRpcNativeCallAdapter{}
		}
		if err := adapter.SendReply(stream, wrapper, request.GetChunkSize()); err != nil {
			return err
		}
	}

	return nil
}

func (s *GrpcBlockchainService) NativeSubscribe(request *dshackle.NativeSubscribeRequest, stream dshackle.Blockchain_NativeSubscribeServer) error {
	if err := s.sessionAuth.requireSession(stream.Context()); err != nil {
		return err
	}
	if request == nil {
		return status.Error(codes.Internal, "request is nil")
	}
	if s.appCtx == nil || s.appCtx.UpstreamSupervisor == nil {
		return status.Error(codes.Unavailable, "upstream supervisor is not configured")
	}

	configuredChain, chainSupervisor := s.resolveChain(request.GetChain())
	if configuredChain == nil {
		return status.Error(codes.Unavailable, fmt.Sprintf("chain %d is not supported", request.GetChain()))
	}
	if chainSupervisor == nil {
		return status.Error(codes.Unavailable, protocol.NoAvailableUpstreamsError().Message)
	}

	if !subscribeMethodSupported(chainSupervisor, request.GetMethod()) {
		return status.Error(codes.Unimplemented, fmt.Sprintf("subscribe %s is not supported for chain %d", request.GetMethod(), request.GetChain()))
	}

	mappedMethod, mappedPayload, err := mapNativeSubscribeMethod(configuredChain.MethodSpec, chainSupervisor, request.GetMethod(), request.GetPayload())
	if err != nil {
		if errors.Is(err, errSubscribeMappingNotSupported) {
			return status.Error(codes.Unimplemented, err.Error())
		}
		return status.Error(codes.Internal, err.Error())
	}

	jsonRpcRequestBody := protocol.JsonRpcRequestBody{Id: []byte("0"), Method: mappedMethod, Params: mappedPayload}
	subscribeRequest := protocol.NewUpstreamJsonRpcRequest("0", jsonRpcRequestBody, true, configuredChain.MethodSpec, mapDshackleSelectors([]*dshackle.Selector{request.GetSelector()})...)
	subCtx := flow.NewSubCtx().WithSubscriptionResultOnly(true)

	executionFlow := flow.NewBaseExecutionFlow(
		configuredChain.Chain,
		s.appCtx.UpstreamSupervisor,
		s.appCtx.CacheProcessor,
		s.appCtx.Registry,
		s.appCtx.AppConfig,
		subCtx,
		s.appCtx.QuorumRegistry,
		s.appCtx.SubEngineRegistry,
	)
	executionFlow.AddHooks(flow.NewMethodBanHook(s.appCtx.UpstreamSupervisor))

	go executionFlow.Execute(stream.Context(), []protocol.RequestHolder{subscribeRequest})

	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	lastSent := time.Now()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case wrapper, ok := <-executionFlow.GetResponses():
			if !ok {
				return nil
			}
			if wrapper == nil || wrapper.Response == nil {
				return status.Error(codes.Internal, "subscription response is empty")
			}
			if wrapper.Response.HasError() {
				return mapNativeSubscribeError(wrapper.Response.GetError())
			}

			subscriptionResponse, ok := wrapper.Response.(protocol.SubscriptionResponseHolder)
			if !ok {
				return status.Error(codes.Internal, "unexpected subscription response type")
			}
			if !subscriptionResponse.IsEventFrame() {
				continue
			}

			if err := stream.Send(&dshackle.NativeSubscribeReplyItem{
				Payload:    subscriptionResponse.ResponseResult(),
				UpstreamId: wrapper.UpstreamId,
			}); err != nil {
				return err
			}
			lastSent = time.Now()
		case <-ticker.C:
			if time.Since(lastSent) >= s.heartbeatInterval {
				if err := stream.Send(&dshackle.NativeSubscribeReplyItem{Heartbeat: true}); err != nil {
					return err
				}
				lastSent = time.Now()
			}
		}
	}
}

func (s *GrpcBlockchainService) resolveChain(chainRef dshackle.ChainRef) (*chains.ConfiguredChain, upstreams.ChainSupervisor) {
	configuredChain := chains.GetChainByGrpcId(int(chainRef))
	if configuredChain == nil || configuredChain.Chain < 0 {
		return nil, nil
	}
	if s.appCtx == nil || s.appCtx.UpstreamSupervisor == nil {
		return configuredChain, nil
	}
	return configuredChain, s.appCtx.UpstreamSupervisor.GetChainSupervisor(configuredChain.Chain)
}

func (s *GrpcBlockchainService) buildNativeCallRequests(
	configuredChain *chains.ConfiguredChain,
	request *dshackle.NativeCallRequest,
) ([]protocol.RequestHolder, map[string]nativeCallAdapter, []*dshackle.NativeCallReplyItem) {
	requests := make([]protocol.RequestHolder, 0, len(request.GetItems()))
	adapters := make(map[string]nativeCallAdapter, len(request.GetItems()))
	preResponses := make([]*dshackle.NativeCallReplyItem, 0)

	for _, item := range request.GetItems() {
		adapter := adapterFor(item)
		builtRequest, failure := adapter.BuildRequest(configuredChain, item, request.GetSelector(), request.GetChunkSize())
		if failure != nil {
			preResponses = append(preResponses, failure)
			continue
		}
		requests = append(requests, builtRequest)
		adapters[builtRequest.Id()] = adapter
	}

	return requests, adapters, preResponses
}

func nativeCallSuccessItems(
	requestID uint32,
	upstreamID string,
	payload []byte,
	chunkSize uint32,
	headers http.Header,
) []*dshackle.NativeCallReplyItem {
	responseHeaders := mapHeaders(headers)
	if chunkSize == 0 || len(payload) <= int(chunkSize) {
		return []*dshackle.NativeCallReplyItem{
			{
				Id:              requestID,
				Succeed:         true,
				Payload:         payload,
				UpstreamId:      upstreamID,
				ResponseHeaders: responseHeaders,
			},
		}
	}

	replyItems := make([]*dshackle.NativeCallReplyItem, 0, len(payload)/int(chunkSize)+1)
	for start := 0; start < len(payload); start += int(chunkSize) {
		end := start + int(chunkSize)
		if end > len(payload) {
			end = len(payload)
		}
		item := &dshackle.NativeCallReplyItem{
			Id:         requestID,
			Succeed:    true,
			Payload:    payload[start:end],
			Chunked:    true,
			FinalChunk: end == len(payload),
		}
		// Response-level metadata travels on the first chunk only.
		if start == 0 {
			item.UpstreamId = upstreamID
			item.ResponseHeaders = responseHeaders
		}
		replyItems = append(replyItems, item)
	}
	return replyItems
}

// nativeCallChunkEmitter forwards a byte stream to the client as
// NativeCallReplyItems without re-framing: each chunk (i.e. each upstream read)
// is emitted the moment it arrives, so bytes reach the client with no added
// latency. The emit callback receives first=true on the very first chunk so the
// caller can stamp response-level metadata once instead of on every chunk.
//
// End-of-stream is folded into the last data chunk when the producer can detect
// it: WriteChunk(p, true) marks p as the final chunk. Producers that can't tell
// which write is the last (the io.Writer / io.Copy path) leave finality to
// Finish, which then sends a trailing empty final chunk as a fallback.
//
// Each emitted slice aliases the caller's buffer with no copy. This is safe
// because gRPC's stream.Send marshals the payload synchronously before
// returning, so an emitted slice never has to outlive its emit call.
type nativeCallChunkEmitter struct {
	emitted   bool
	finalSent bool
	emit      func(chunk []byte, first, final bool) error
}

func newNativeCallChunkEmitter(emit func(chunk []byte, first, final bool) error) *nativeCallChunkEmitter {
	return &nativeCallChunkEmitter{emit: emit}
}

// Write implements io.Writer for producers that can't signal the last write
// (the REST passthrough path via io.Copy). Every chunk is non-final; the
// trailing final marker is left to Finish.
func (e *nativeCallChunkEmitter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := e.WriteChunk(p, false); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WriteChunk emits one chunk, marking it final when the producer knows the
// response body has ended. Empty non-final writes are skipped; an empty final
// write is allowed so end-of-stream can be signalled with no payload.
func (e *nativeCallChunkEmitter) WriteChunk(p []byte, final bool) error {
	if len(p) == 0 && !final {
		return nil
	}
	first := !e.emitted
	e.emitted = true
	if final {
		e.finalSent = true
	}
	return e.emit(p, first, final)
}

// Finish guarantees the client sees exactly one final chunk. If a chunk was
// already marked final inline, it is a no-op; otherwise it sends the terminal
// empty payload with final=true (the fallback for the io.Writer path and for an
// empty body).
func (e *nativeCallChunkEmitter) Finish() error {
	if e.finalSent {
		return nil
	}
	return e.WriteChunk(nil, true)
}

func nativeCallErrorItem(
	requestID uint32,
	responseError *protocol.ResponseError,
	upstreamID string,
	errorAsIs []byte,
	headers http.Header,
) *dshackle.NativeCallReplyItem {
	if responseError == nil {
		responseError = protocol.ServerError()
	}

	replyItem := &dshackle.NativeCallReplyItem{
		Id:              requestID,
		Succeed:         false,
		ErrorMessage:    responseError.Message,
		ItemErrorCode:   int32(responseError.Code),
		UpstreamId:      upstreamID,
		ResponseHeaders: mapHeaders(headers),
	}
	if responseError.Data != nil {
		replyItem.ErrorData = nativeCallErrorData(responseError.Data)
	}
	if len(errorAsIs) > 0 {
		replyItem.ErrorAsIs = append([]byte(nil), errorAsIs...)
	}

	return replyItem
}

func mapHeaders(headers http.Header) []*dshackle.KeyValue {
	keyValueHeaders := make([]*dshackle.KeyValue, 0, len(headers))
	for key, values := range headers {
		for _, value := range values {
			keyValueHeaders = append(keyValueHeaders, &dshackle.KeyValue{Key: key, Value: value})
		}
	}

	return keyValueHeaders
}

func nativeCallErrorData(data any) string {
	switch value := data.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		result, err := sonic.Marshal(value)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}
		return string(result)
	}
}

func parseCallItemID(requestID string) uint32 {
	if requestID == "" {
		return 0
	}
	id, err := strconv.ParseUint(requestID, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(id)
}

func mapNativeSubscribeMethod(
	methodSpecName string,
	chainSupervisor upstreams.ChainSupervisor,
	requestedMethod string,
	payload []byte,
) (string, []byte, error) {
	if supportsNativeSubscribeMethod(methodSpecName, requestedMethod) {
		return normalizeNativeSubscribePayload(requestedMethod, payload)
	}
	if !supportsEthSubscribeFallback(methodSpecName, chainSupervisor) {
		return "", nil, fmt.Errorf("%w: subscribe %s is not supported for chain spec %s", errSubscribeMappingNotSupported, requestedMethod, methodSpecName)
	}
	return mapToEthSubscribeFallback(requestedMethod, payload)
}

func supportsNativeSubscribeMethod(methodSpecName string, requestedMethod string) bool {
	return specs.IsSubscribeMethod(methodSpecName, requestedMethod)
}

func subscribeMethodSupported(chainSupervisor upstreams.ChainSupervisor, method string) bool {
	subMethods := chainSupervisor.GetChainState().SubMethods
	return subMethods != nil && subMethods.ContainsOne(method)
}

func normalizeNativeSubscribePayload(requestedMethod string, payload []byte) (string, []byte, error) {
	if len(payload) == 0 {
		return requestedMethod, []byte("[]"), nil
	}
	if !json.Valid(payload) {
		return "", nil, fmt.Errorf("invalid subscribe payload format")
	}
	return requestedMethod, payload, nil
}

func supportsEthSubscribeFallback(methodSpecName string, chainSupervisor upstreams.ChainSupervisor) bool {
	ethSubscribeSupported := specs.IsSubscribeMethod(methodSpecName, "eth_subscribe")
	if !ethSubscribeSupported && chainSupervisor != nil {
		ethSubscribeSupported = chainSupervisor.GetMethod("eth_subscribe") != nil
	}
	return ethSubscribeSupported
}

func mapToEthSubscribeFallback(requestedMethod string, payload []byte) (string, []byte, error) {
	mappedParams, err := mapEthSubscribeParams(requestedMethod, payload)
	if err != nil {
		return "", nil, err
	}
	return "eth_subscribe", mappedParams, nil
}

func mapEthSubscribeParams(requestedMethod string, payload []byte) ([]byte, error) {
	methodRaw, _ := sonic.Marshal(requestedMethod)
	params := []json.RawMessage{methodRaw}

	// dproxy NativeSubscribe sends Method as the concrete subscription type and
	// Payload as that type's single parameter payload. For example:
	//   Method="logs", Payload={...} -> eth_subscribe params ["logs", {...}]
	//   Method="newHeads", Payload=null -> eth_subscribe params ["newHeads"]
	if len(payload) > 0 && string(payload) != "null" {
		if !json.Valid(payload) {
			return nil, fmt.Errorf("invalid subscribe payload format")
		}
		params = append(params, append(json.RawMessage(nil), payload...))
	}

	result, err := sonic.Marshal(params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func mapNativeSubscribeError(responseError *protocol.ResponseError) error {
	if responseError == nil {
		return status.Error(codes.Internal, "internal server error")
	}

	switch responseError.Code {
	case protocol.NoAvailableUpstreams, protocol.WrongChain:
		return status.Error(codes.Unavailable, responseError.Message)
	case protocol.NoSupportedMethod:
		return status.Error(codes.Unimplemented, responseError.Message)
	case protocol.AuthErrorCode:
		return status.Error(codes.Unauthenticated, responseError.Message)
	default:
		if strings.Contains(strings.ToLower(responseError.Message), "subscription request") &&
			strings.Contains(strings.ToLower(responseError.Message), "unable to process") {
			return status.Error(codes.Unimplemented, responseError.Message)
		}
		return status.Error(codes.Internal, responseError.Message)
	}
}
