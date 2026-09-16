package emerald

import (
	"fmt"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/drpcorg/nodecore/internal/signature"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/flow"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	_ "google.golang.org/grpc/encoding/gzip"
)

const defaultNativeSubscribeHeartbeat = 30 * time.Second

type GrpcBlockchainService struct {
	dshackle.UnimplementedBlockchainServer

	appCtx            *server_ctx.ApplicationServerContext
	sessionAuth       *grpcSessionAuth
	signer            signature.ResponseSigner
	heartbeatInterval time.Duration
}

func NewGrpcBlockchainService(
	appCtx *server_ctx.ApplicationServerContext,
	sessionAuth *grpcSessionAuth,
	signer signature.ResponseSigner,
) *GrpcBlockchainService {
	return &GrpcBlockchainService{
		appCtx:            appCtx,
		sessionAuth:       sessionAuth,
		signer:            signer,
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
		// no items to correlate with or to pick a vocabulary from: this is the
		// one failure a client can only observe as id 0 with a nodecore code
		return stream.Send(withNoUpstreamId(nativeCallErrorItem(0, protocol.ClientError(fmt.Errorf("request is nil")), nil)))
	}
	if s.appCtx == nil || s.appCtx.UpstreamSupervisor == nil {
		return sendRequestFailure(stream, request, protocol.NoAvailableUpstreamsError())
	}

	configuredChain, chainSupervisor := s.resolveChain(request.GetChain())
	if configuredChain == nil {
		return sendRequestFailure(stream, request, protocol.WrongChainError(strconv.Itoa(int(request.GetChain()))))
	}
	if chainSupervisor == nil {
		return sendRequestFailure(stream, request, protocol.NoAvailableUpstreamsError())
	}

	requests, items, preResponses := s.buildNativeCallRequests(configuredChain, request)
	for _, preResponse := range preResponses {
		if err := stream.Send(preResponse); err != nil {
			return err
		}
	}
	if len(requests) == 0 {
		return nil
	}

	executionFlow := flow.NewGenericExecutionFlow(
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
		item, ok := items[wrapper.RequestId]
		if !ok {
			// Without the originating item we don't know its nonce, so a success
			// here could silently drop a signature the client asked for. The item
			// kind is unknown too (this lookup is what failed), so - as a
			// documented exception to the per-kind error vocabulary - the failure
			// is rendered with the nodecore code 500. Defensive: the flow only
			// answers ids this loop built.
			log.Warn().Msgf("no request found for id %s, cannot build a reply", wrapper.RequestId)
			replyItem := nativeCallErrorItem(parseCallItemID(wrapper.RequestId), protocol.ServerError(), nil)
			replyItem.UpstreamId = wrapper.UpstreamId
			if err := stream.Send(replyItem); err != nil {
				return err
			}
			continue
		}
		if err := item.adapter.SendReply(stream, wrapper, item.nonce, s.signer); err != nil {
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

	adapter := subscribeAdapterFor(configuredChain.MethodSpec, request.GetMethod())
	subscribeRequest, err := adapter.BuildRequest(configuredChain, chainSupervisor, request)
	if err != nil {
		return err
	}

	nonce := request.GetNonce()
	if err := signingUnavailable(nonce, s.signer); err != nil {
		log.Warn().Msg("a subscription requested a signature but response signing is not configured")
		return status.Error(codes.Internal, err.Error())
	}

	subCtx := flow.NewSubCtx().WithSubscriptionResultOnly(true)
	executionFlow := flow.NewGenericExecutionFlow(
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

	return serveNativeSubscribe(stream, executionFlow.GetResponses(), adapter, nonce, s.signer, s.heartbeatInterval)
}

// serveNativeSubscribe forwards the flow's responses through the adapter until
// the client goes away, the flow closes the channel, or the adapter reports
// the subscription over. Heartbeats keep an idle stream visibly alive; they
// carry nothing but the flag. A nil wrapper or response is a flow bug and is
// reported as Internal.
func serveNativeSubscribe(
	stream dshackle.Blockchain_NativeSubscribeServer,
	responses <-chan *protocol.ResponseHolderWrapper,
	adapter nativeSubscribeAdapter,
	nonce uint64,
	signer signature.ResponseSigner,
	heartbeatInterval time.Duration,
) error {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	lastSent := time.Now()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case wrapper, ok := <-responses:
			if !ok {
				return nil
			}
			if wrapper == nil || wrapper.Response == nil {
				return status.Error(codes.Internal, "subscription response is empty")
			}
			done, err := adapter.SendReply(stream, wrapper, nonce, signer)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			lastSent = time.Now()
		case <-ticker.C:
			if time.Since(lastSent) >= heartbeatInterval {
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

// nativeCallItem is what a request id resolves to while replies stream back:
// the adapter that built the request and the nonce the client asked us to sign
// the reply with (0 meaning "do not sign").
type nativeCallItem struct {
	adapter nativeCallAdapter
	nonce   uint64
}

func (s *GrpcBlockchainService) buildNativeCallRequests(
	configuredChain *chains.ConfiguredChain,
	request *dshackle.NativeCallRequest,
) ([]protocol.RequestHolder, map[string]nativeCallItem, []*dshackle.NativeCallReplyItem) {
	requests := make([]protocol.RequestHolder, 0, len(request.GetItems()))
	items := make(map[string]nativeCallItem, len(request.GetItems()))
	preResponses := make([]*dshackle.NativeCallReplyItem, 0)

	for _, item := range request.GetItems() {
		adapter := adapterFor(item)
		if err := signingUnavailable(item.GetNonce(), s.signer); err != nil {
			log.Warn().Msgf("item %d requested a signature but response signing is not configured", item.GetId())
			preResponses = append(preResponses, withNoUpstreamId(adapter.ErrorItem(item.GetId(), protocol.ServerErrorWithCause(err), nil)))
			continue
		}
		builtRequest, failure := adapter.BuildRequest(configuredChain, item, request.GetSelector(), request.GetChunkSize())
		if failure != nil {
			preResponses = append(preResponses, failure)
			continue
		}
		requests = append(requests, builtRequest)
		items[builtRequest.Id()] = nativeCallItem{adapter: adapter, nonce: item.GetNonce()}
	}

	return requests, items, preResponses
}

// nativeCallSuccessItem builds the reply for a fully-buffered response. A
// buffered response is always a single unchunked item however large: chunk_size
// selects a streaming request, it is not a framing size for in-memory payloads.
// Chunked replies come from streamNativeCallBody instead.
func nativeCallSuccessItem(requestID uint32, payload []byte) *dshackle.NativeCallReplyItem {
	return &dshackle.NativeCallReplyItem{
		Id:      requestID,
		Succeed: true,
		Payload: payload,
	}
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

func nativeCallErrorItem(requestID uint32, responseError *protocol.ResponseError, errorAsIs []byte) *dshackle.NativeCallReplyItem {
	if responseError == nil {
		responseError = protocol.ServerError()
	}

	replyItem := &dshackle.NativeCallReplyItem{
		Id:            requestID,
		Succeed:       false,
		ErrorMessage:  responseError.Message,
		ItemErrorCode: int32(responseError.Code),
	}
	if responseError.Data != nil {
		replyItem.ErrorData = nativeCallErrorData(responseError.Data)
	}
	if len(errorAsIs) > 0 {
		replyItem.ErrorAsIs = append([]byte(nil), errorAsIs...)
	}

	return replyItem
}

// sendRequestFailure answers a request-level failure (unknown chain, no
// upstreams) once per item, each in its own kind's error vocabulary and under
// its own id - a gRPC item must see a canonical code, and id 0 would match
// nothing the client sent.
func sendRequestFailure(
	stream dshackle.Blockchain_NativeCallServer,
	request *dshackle.NativeCallRequest,
	responseError *protocol.ResponseError,
) error {
	for _, item := range request.GetItems() {
		if err := stream.Send(withNoUpstreamId(adapterFor(item).ErrorItem(item.GetId(), responseError, nil))); err != nil {
			return err
		}
	}
	return nil
}

func mapHeaders[M ~map[string][]string](headers M) []*dshackle.KeyValue {
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

func subscribeMethodSupported(chainSupervisor upstreams.ChainSupervisor, method string) bool {
	subMethods := chainSupervisor.GetChainState().SubMethods
	return subMethods != nil && subMethods.ContainsOne(method)
}
