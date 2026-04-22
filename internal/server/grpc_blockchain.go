package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
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

	appCtx            *ApplicationContext
	sessionAuth       *grpcSessionAuth
	heartbeatInterval time.Duration
}

func NewGrpcBlockchainService(appCtx *ApplicationContext, sessionAuth *grpcSessionAuth) *GrpcBlockchainService {
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
	if s.appCtx == nil || s.appCtx.upstreamSupervisor == nil {
		return status.Error(codes.Unavailable, "upstream supervisor is not configured")
	}

	return emerald.SubscribeChainStatus(s.appCtx.upstreamSupervisor, stream)
}

func (s *GrpcBlockchainService) NativeCall(request *dshackle.NativeCallRequest, stream dshackle.Blockchain_NativeCallServer) error {
	if err := s.sessionAuth.requireSession(stream.Context()); err != nil {
		return err
	}
	if request == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.ClientError(fmt.Errorf("request is nil")), flow.NoUpstream, nil))
	}
	if s.appCtx == nil || s.appCtx.upstreamSupervisor == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.NoAvailableUpstreamsError(), flow.NoUpstream, nil))
	}

	configuredChain, chainSupervisor := s.resolveChain(request.GetChain())
	if configuredChain == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.WrongChainError(strconv.Itoa(int(request.GetChain()))), flow.NoUpstream, nil))
	}
	if chainSupervisor == nil {
		return stream.Send(nativeCallErrorItem(0, protocol.NoAvailableUpstreamsError(), flow.NoUpstream, nil))
	}

	requests, preResponses := s.buildNativeCallRequests(request, chainSupervisor)
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
		s.appCtx.upstreamSupervisor,
		s.appCtx.cacheProcessor,
		s.appCtx.registry,
		s.appCtx.appConfig,
		flow.NewSubCtx(),
		s.appCtx.quorumRegistry,
	)
	executionFlow.AddHooks(flow.NewMethodBanHook(s.appCtx.upstreamSupervisor))

	go executionFlow.Execute(stream.Context(), requests)

	for wrapper := range executionFlow.GetResponses() {
		if err := sendNativeCallReplyItems(stream, wrapper, request.GetChunkSize()); err != nil {
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
	if s.appCtx == nil || s.appCtx.upstreamSupervisor == nil {
		return status.Error(codes.Unavailable, "upstream supervisor is not configured")
	}

	configuredChain, chainSupervisor := s.resolveChain(request.GetChain())
	if configuredChain == nil {
		return status.Error(codes.Unavailable, fmt.Sprintf("chain %d is not supported", request.GetChain()))
	}
	if chainSupervisor == nil {
		return status.Error(codes.Unavailable, protocol.NoAvailableUpstreamsError().Message)
	}

	mappedMethod, mappedPayload, err := mapNativeSubscribeMethod(configuredChain.MethodSpec, chainSupervisor, request.GetMethod(), request.GetPayload())
	if err != nil {
		if errors.Is(err, errSubscribeMappingNotSupported) {
			return status.Error(codes.Unimplemented, err.Error())
		}
		return status.Error(codes.Internal, err.Error())
	}

	specMethod := chainSupervisor.GetMethod(mappedMethod)
	subscribeRequest := protocol.NewUpstreamJsonRpcRequest(
		"0",
		[]byte("0"),
		mappedMethod,
		mappedPayload,
		true,
		specMethod,
	)
	subCtx := flow.NewSubCtx().WithSubscriptionResultOnly(true)

	executionFlow := flow.NewBaseExecutionFlow(
		configuredChain.Chain,
		s.appCtx.upstreamSupervisor,
		s.appCtx.cacheProcessor,
		s.appCtx.registry,
		s.appCtx.appConfig,
		subCtx,
		s.appCtx.quorumRegistry,
	)
	executionFlow.AddHooks(flow.NewMethodBanHook(s.appCtx.upstreamSupervisor))

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
			// Skip subscription ACK frame. dproxy expects only event payload.
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
	if s.appCtx == nil || s.appCtx.upstreamSupervisor == nil {
		return configuredChain, nil
	}
	return configuredChain, s.appCtx.upstreamSupervisor.GetChainSupervisor(configuredChain.Chain)
}

func (s *GrpcBlockchainService) buildNativeCallRequests(
	request *dshackle.NativeCallRequest,
	chainSupervisor upstreams.ChainSupervisor,
) ([]protocol.RequestHolder, []*dshackle.NativeCallReplyItem) {
	requests := make([]protocol.RequestHolder, 0, len(request.GetItems()))
	preResponses := make([]*dshackle.NativeCallReplyItem, 0)

	for _, item := range request.GetItems() {
		if item.GetRestData() != nil {
			preResponses = append(preResponses, nativeCallErrorItem(
				item.GetId(),
				protocol.ClientError(fmt.Errorf("rest_data is not supported")),
				flow.NoUpstream,
				nil,
			))
			continue
		}

		payload := item.GetPayload()
		if len(payload) == 0 {
			payload = []byte("[]")
		}
		if !json.Valid(payload) {
			preResponses = append(preResponses, nativeCallErrorItem(
				item.GetId(),
				protocol.ClientError(fmt.Errorf("payload is not a valid JSON value")),
				flow.NoUpstream,
				nil,
			))
			continue
		}

		specMethod := (*specs.Method)(nil)
		if chainSupervisor != nil {
			specMethod = chainSupervisor.GetMethod(item.GetMethod())
		}
		requestID := strconv.FormatUint(uint64(item.GetId()), 10)
		if request.ChunkSize > 0 {
			requests = append(requests, protocol.NewStreamUpstreamJsonRpcRequest(
				requestID,
				[]byte(requestID),
				item.GetMethod(),
				payload,
				specMethod,
			))
		} else {
			requests = append(requests, protocol.NewUpstreamJsonRpcRequest(
				requestID,
				[]byte(requestID),
				item.GetMethod(),
				payload,
				false,
				specMethod,
			))
		}
	}

	return requests, preResponses
}

func sendNativeCallReplyItems(
	stream dshackle.Blockchain_NativeCallServer,
	wrapper *protocol.ResponseHolderWrapper,
	chunkSize uint32,
) error {
	if wrapper == nil || wrapper.Response == nil {
		return fmt.Errorf("response wrapper is empty")
	}

	requestID := parseCallItemID(wrapper.RequestId)
	if wrapper.Response.HasError() {
		return stream.Send(nativeCallErrorItem(requestID, wrapper.Response.GetError(), wrapper.UpstreamId, wrapper.Response.ResponseResult()))
	}

	if wrapper.Response.HasStream() {
		err := streamNativeCallPayload(
			requestID,
			wrapper.UpstreamId,
			wrapper.Response.EncodeResponse([]byte("0")),
			chunkSize,
			func(item *dshackle.NativeCallReplyItem) error {
				return stream.Send(item)
			},
		)
		if err != nil {
			return stream.Send(nativeCallErrorItem(requestID, protocol.ServerErrorWithCause(err), wrapper.UpstreamId, nil))
		}
		return nil
	}

	payload := append([]byte(nil), wrapper.Response.ResponseResult()...)
	for _, replyItem := range nativeCallSuccessItems(requestID, wrapper.UpstreamId, payload, chunkSize) {
		if err := stream.Send(replyItem); err != nil {
			return err
		}
	}
	return nil
}

func streamNativeCallPayload(
	requestID uint32,
	upstreamID string,
	reader io.Reader,
	chunkSize uint32,
	send func(*dshackle.NativeCallReplyItem) error,
) error {
	effectiveChunkSize := int(chunkSize)
	if effectiveChunkSize <= 0 {
		effectiveChunkSize = protocol.MaxChunkSize
	}
	emitter := newNativeCallChunkEmitter(effectiveChunkSize, func(chunk []byte, final bool) error {
		return send(&dshackle.NativeCallReplyItem{
			Id:         requestID,
			Succeed:    true,
			Payload:    chunk,
			Chunked:    true,
			FinalChunk: final,
			UpstreamId: upstreamID,
		})
	})
	if err := streamJsonRPCResult(reader, emitter); err != nil {
		return err
	}
	return emitter.Flush()
}

func nativeCallSuccessItems(
	requestID uint32,
	upstreamID string,
	payload []byte,
	chunkSize uint32,
) []*dshackle.NativeCallReplyItem {
	if chunkSize == 0 || len(payload) <= int(chunkSize) {
		return []*dshackle.NativeCallReplyItem{
			{
				Id:         requestID,
				Succeed:    true,
				Payload:    payload,
				UpstreamId: upstreamID,
			},
		}
	}

	replyItems := make([]*dshackle.NativeCallReplyItem, 0, len(payload)/int(chunkSize)+1)
	for start := 0; start < len(payload); start += int(chunkSize) {
		end := start + int(chunkSize)
		if end > len(payload) {
			end = len(payload)
		}
		replyItems = append(replyItems, &dshackle.NativeCallReplyItem{
			Id:         requestID,
			Succeed:    true,
			Payload:    payload[start:end],
			Chunked:    true,
			FinalChunk: end == len(payload),
			UpstreamId: upstreamID,
		})
	}
	return replyItems
}

type nativeCallChunkEmitter struct {
	chunkSize int
	pending   []byte
	lastChunk []byte
	hasLast   bool
	emit      func([]byte, bool) error
}

func newNativeCallChunkEmitter(chunkSize int, emit func([]byte, bool) error) *nativeCallChunkEmitter {
	return &nativeCallChunkEmitter{
		chunkSize: chunkSize,
		pending:   make([]byte, 0, chunkSize),
		emit:      emit,
	}
}

func (e *nativeCallChunkEmitter) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		available := e.chunkSize - len(e.pending)
		if available > len(p) {
			available = len(p)
		}
		e.pending = append(e.pending, p[:available]...)
		p = p[available:]
		if len(e.pending) == e.chunkSize {
			if err := e.pushChunk(append([]byte(nil), e.pending...)); err != nil {
				return 0, err
			}
			e.pending = e.pending[:0]
		}
	}
	return written, nil
}

func (e *nativeCallChunkEmitter) Flush() error {
	if len(e.pending) > 0 {
		if err := e.pushChunk(append([]byte(nil), e.pending...)); err != nil {
			return err
		}
		e.pending = e.pending[:0]
	}
	if e.hasLast {
		return e.emit(e.lastChunk, true)
	}
	return e.emit([]byte{}, true)
}

func (e *nativeCallChunkEmitter) pushChunk(chunk []byte) error {
	if e.hasLast {
		if err := e.emit(e.lastChunk, false); err != nil {
			return err
		}
	}
	e.lastChunk = chunk
	e.hasLast = true
	return nil
}

func nativeCallErrorItem(
	requestID uint32,
	responseError *protocol.ResponseError,
	upstreamID string,
	errorAsIs []byte,
) *dshackle.NativeCallReplyItem {
	if responseError == nil {
		responseError = protocol.ServerError()
	}

	replyItem := &dshackle.NativeCallReplyItem{
		Id:            requestID,
		Succeed:       false,
		ErrorMessage:  responseError.Message,
		ItemErrorCode: int32(responseError.Code),
		UpstreamId:    upstreamID,
	}
	if responseError.Data != nil {
		replyItem.ErrorData = nativeCallErrorData(responseError.Data)
	}
	if len(errorAsIs) > 0 {
		replyItem.ErrorAsIs = append([]byte(nil), errorAsIs...)
	}

	return replyItem
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
	args := make([]json.RawMessage, 0)
	if len(payload) > 0 {
		if !json.Valid(payload) {
			return nil, fmt.Errorf("invalid subscribe payload format")
		}
		if err := json.Unmarshal(payload, &args); err != nil {
			return nil, fmt.Errorf("subscribe payload must be a JSON array")
		}
	}

	methodRaw, _ := json.Marshal(requestedMethod)
	params := make([]json.RawMessage, 0, 1+len(args))
	params = append(params, methodRaw)
	params = append(params, args...)

	result, err := json.Marshal(params)
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
