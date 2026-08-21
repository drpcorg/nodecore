package connectors

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// GrpcConnector sends unary gRPC calls to an upstream over one long-lived
// ClientConn (HTTP/2 multiplexing + keepalives). The traffic path is
// bytes-only: request and response messages pass through a raw codec and are
// never parsed. Streaming calls are a follow-up task.
type GrpcConnector struct {
	endpoint       string
	upstreamId     string
	conn           *grpc.ClientConn
	requestTimeout time.Duration
	// additionalMetadata holds the connector config headers, sent as
	// per-call metadata; keys are lowercase per gRPC metadata convention.
	additionalMetadata     map[string]string
	deniedResponseMetadata mapset.Set[string]
}

// grpcRequestMetadataDeny is the request-side deny list: hop-by-hop keys
// (mirroring the HTTP connector's discipline) plus keys the gRPC transport
// owns for the *outgoing* call. The reserved "grpc-*" family is denied by
// prefix in the send path.
var grpcRequestMetadataDeny = mapset.NewThreadUnsafeSet(
	"connection",
	"keep-alive",
	"proxy-authenticate",
	"proxy-authorization",
	"te",
	"trailer",
	"trailers",
	"transfer-encoding",
	"upgrade",
	"host",
	"content-length",
	"content-type",
	"accept-encoding",
	"user-agent",
)

const (
	// grpcRequestTimeout caps every unary call, mirroring the HTTP connector's
	// 60s client timeout. A context deadline can only tighten down the chain,
	// so probes (internalTimeout) and clients with their own gRPC deadlines
	// are unaffected - the cap only bounds callers with no deadline at all.
	// It propagates to the node as grpc-timeout, so the upstream can abort
	// server-side work early.
	grpcRequestTimeout = 60 * time.Second
	// grpcMaxRecvMsgSize lifts grpc-go's 4MB default receive cap: a proxy must
	// not impose a lower response-size ceiling than the node itself allows
	// (large checkpoints/objects easily exceed 4MB).
	grpcMaxRecvMsgSize = 32 << 20
)

// rawGrpcMessage is the private carrier type the raw codec dispatches on.
type rawGrpcMessage struct {
	data []byte
}

// rawGrpcCodec passes message bytes through untouched while reporting
// Name() == "proto", so the content-type stays application/grpc+proto and
// the node's stubs decode our bytes as the normal protobuf they are.
type rawGrpcCodec struct{}

func (rawGrpcCodec) Marshal(v any) ([]byte, error) {
	msg, ok := v.(*rawGrpcMessage)
	if !ok {
		return nil, fmt.Errorf("grpc raw codec can only marshal *rawGrpcMessage, got %T", v)
	}
	return msg.data, nil
}

func (rawGrpcCodec) Unmarshal(data []byte, v any) error {
	msg, ok := v.(*rawGrpcMessage)
	if !ok {
		return fmt.Errorf("grpc raw codec can only unmarshal into *rawGrpcMessage, got %T", v)
	}
	msg.data = data
	return nil
}

func (rawGrpcCodec) Name() string {
	return "proto"
}

func NewGrpcConnector(connectorConfig *config.ApiConnectorConfig, upstreamId string) (*GrpcConnector, error) {
	endpoint, err := url.Parse(connectorConfig.Url)
	if err != nil {
		return nil, fmt.Errorf("error parsing the endpoint: %v", err)
	}
	creds, err := grpcTransportCredentials(connectorConfig, endpoint.Scheme)
	if err != nil {
		return nil, err
	}
	// the default reconnect backoff climbs to 120s between attempts; a load
	// balancer wants a recovered upstream back in rotation sooner
	reconnectBackoff := backoff.DefaultConfig
	reconnectBackoff.MaxDelay = 30 * time.Second
	conn, err := grpc.NewClient(
		endpoint.Host,
		grpc.WithTransportCredentials(creds),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           reconnectBackoff,
			MinConnectTimeout: 10 * time.Second,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't create a grpc client: %v", err)
	}
	return NewGrpcConnectorWithClientConn(conn, connectorConfig, upstreamId), nil
}

// NewGrpcConnectorWithClientConn wraps an already-dialed ClientConn (used by
// tests to connect over bufconn).
func NewGrpcConnectorWithClientConn(conn *grpc.ClientConn, connectorConfig *config.ApiConnectorConfig, upstreamId string) *GrpcConnector {
	return &GrpcConnector{
		endpoint:               connectorConfig.Url,
		upstreamId:             upstreamId,
		conn:                   conn,
		requestTimeout:         grpcRequestTimeout,
		additionalMetadata:     lowercaseKeys(connectorConfig.Headers),
		deniedResponseMetadata: buildDeniedGrpcResponseMetadata(connectorConfig.ResponseHeaderDeny),
	}
}

func (g *GrpcConnector) SendRequest(ctx context.Context, request protocol.RequestHolder) protocol.ResponseHolder {
	body, err := request.Body()
	if err != nil {
		return clientFailure(request, fmt.Errorf("error parsing a request body: %v", err))
	}

	// cap the call; the parent can only be tightened, never extended, so
	// probe timeouts and client deadlines stay in force
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(ctx, g.requestTimeout)
	defer cancel()
	ctx = g.outgoingMetadataContext(ctx, request)

	var respMsg rawGrpcMessage
	var headerMD, trailerMD metadata.MD
	err = g.conn.Invoke(ctx, request.Method(), &rawGrpcMessage{data: body}, &respMsg,
		grpc.ForceCodec(rawGrpcCodec{}),
		grpc.MaxCallRecvMsgSize(grpcMaxRecvMsgSize),
		grpc.Header(&headerMD),
		grpc.Trailer(&trailerMD),
	)
	if err != nil {
		// only the CALLER giving up (probe timeout, client deadline/disconnect)
		// is a total failure; our own cap expiring falls through to the status
		// path as DEADLINE_EXCEEDED and stays retry/hedge-eligible
		if parentCtx.Err() != nil {
			return protocol.NewTotalFailure(request, protocol.CtxError(fmt.Errorf("upstream %s: %v", g.upstreamId, parentCtx.Err())))
		}
		st, ok := status.FromError(err)
		if !ok {
			// Log the full error for operators; surface only the upstream id to
			// the caller so the URL/host never leaks.
			zerolog.Ctx(ctx).Warn().Err(err).Str("upstream", g.upstreamId).Msg("upstream grpc request failed")
			return protocol.NewPartialFailure(
				request,
				protocol.ServerErrorWithCause(fmt.Errorf("upstream %s request failed", g.upstreamId)),
			)
		}
		grpcStatus := &protocol.GrpcStatus{Code: st.Code(), Message: st.Message()}
		if len(st.Details()) > 0 {
			if statusProto, marshalErr := proto.Marshal(st.Proto()); marshalErr == nil {
				grpcStatus.StatusProto = statusProto
			}
		}
		// error replies carry the upstream metadata too - RESOURCE_EXHAUSTED
		// trailers (rate-limit hints) are exactly the ones a client needs
		response := protocol.NewGrpcUpstreamErrorResponse(request, grpcStatus)
		switch resp := response.(type) {
		case *protocol.GenericUpstreamResponse:
			resp.WithResponseHeaders(g.filterResponseMetadata(headerMD)).
				WithResponseTrailers(g.filterResponseMetadata(trailerMD))
		case *protocol.ReplyError:
			resp.WithResponseHeaders(g.filterResponseMetadata(headerMD)).
				WithResponseTrailers(g.filterResponseMetadata(trailerMD))
		}
		return response
	}

	return protocol.NewGrpcUpstreamResponse(request.Id(), respMsg.data).
		WithResponseHeaders(g.filterResponseMetadata(headerMD)).
		WithResponseTrailers(g.filterResponseMetadata(trailerMD))
}

func (g *GrpcConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, errors.New("subscriptions are not supported by the grpc connector")
}

func (g *GrpcConnector) Unsubscribe(_ string) {
}

func (g *GrpcConnector) GetType() specs.ApiConnectorType {
	return specs.GrpcConnector
}

func (g *GrpcConnector) GetUrl() string {
	return g.endpoint
}

// SubscribeStates returns nil in v1: mapping ClientConn connectivity states
// onto connector-state events differs from the websocket model and is
// deliberately left to a later task.
func (g *GrpcConnector) SubscribeStates(_ string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

func (g *GrpcConnector) Start() {
}

func (g *GrpcConnector) Stop() {
	if err := g.conn.Close(); err != nil {
		zerolog.Ctx(context.Background()).Warn().Err(err).Str("upstream", g.upstreamId).Msg("couldn't close the grpc client conn")
	}
}

func (g *GrpcConnector) Running() bool {
	return true
}

// grpcTransportCredentials picks transport security from the URL scheme:
// "grpc" and "http" mean plaintext, anything else means TLS with the
// system roots or the connector's custom CA.
func grpcTransportCredentials(connectorConfig *config.ApiConnectorConfig, scheme string) (credentials.TransportCredentials, error) {
	if scheme == "grpc" || scheme == "http" {
		return insecure.NewCredentials(), nil
	}
	customCA, err := utils.GetCustomCAPool(connectorConfig.Ca)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{}
	if customCA != nil {
		tlsConfig.RootCAs = customCA
	}
	return credentials.NewTLS(tlsConfig), nil
}

// outgoingMetadataContext forwards the client metadata minus the deny list
// and the reserved "grpc-*" family, then layers the connector's configured
// headers on top - config-owned keys are typically auth tokens a curious
// client must not be able to override.
func (g *GrpcConnector) outgoingMetadataContext(ctx context.Context, request protocol.RequestHolder) context.Context {
	md := metadata.MD{}
	if rp := request.RequestParams(); rp != nil {
		for k, vs := range rp.Headers {
			key := strings.ToLower(k)
			if strings.HasPrefix(key, "grpc-") || grpcRequestMetadataDeny.Contains(key) {
				continue
			}
			if _, taken := g.additionalMetadata[key]; taken {
				continue
			}
			md.Append(key, vs...)
		}
	}
	for k, v := range g.additionalMetadata {
		md.Append(k, v)
	}
	if len(md) == 0 {
		return ctx
	}
	return metadata.NewOutgoingContext(ctx, md)
}

// buildDeniedGrpcResponseMetadata merges the HTTP connector's always-stripped
// response set with the gRPC transport-owned keys (content-type and the
// reserved "grpc-*" family, the latter denied by prefix in the filter) plus
// any operator-supplied additions. Keys are lowercase per metadata convention.
func buildDeniedGrpcResponseMetadata(extra []string) mapset.Set[string] {
	set := mapset.NewThreadUnsafeSet[string]()
	for _, k := range defaultResponseHeaderDeny {
		set.Add(strings.ToLower(k))
	}
	set.Add("content-type")
	for _, k := range extra {
		set.Add(strings.ToLower(k))
	}
	return set
}

// filterResponseMetadata drops transport-owned and denied keys; everything
// else (chain-specific metadata, rate-limit hints, request ids) passes
// through. Keys stay lowercase.
func (g *GrpcConnector) filterResponseMetadata(md metadata.MD) http.Header {
	if len(md) == 0 {
		return nil
	}
	out := make(http.Header, len(md))
	for k, vs := range md {
		key := strings.ToLower(k)
		if strings.HasPrefix(key, "grpc-") || g.deniedResponseMetadata.Contains(key) {
			continue
		}
		out[key] = vs
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func lowercaseKeys(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[strings.ToLower(k)] = v
	}
	return out
}

var _ ApiConnector = (*GrpcConnector)(nil)
