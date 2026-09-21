package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing/iotest"

	"github.com/bytedance/sonic"
)

// Client-facing frames of a subscription/stream. They are produced by the
// SubscriptionRequestProcessor and consumed by the WS server, the emerald
// server and the gRPC chain ingress:
//   - SubscriptionEventResponse: one event, delivered either as the bare
//     payload (result-only consumers) or wrapped in a JSON-RPC notification
//     envelope (the WS server);
//   - SubscriptionEndResponse: the clean end of a bounded stream (carries the
//     upstream trailers, no payload).
//
// The JSON-RPC subscribe ack is a plain WsJsonRpcResponse, not a subscription
// frame.

// SubscriptionEventResponse is one event of a subscription/stream as the
// client receives it. The encoder decides the presentation; the transport
// metadata (gRPC headers/trailers) rides along for the gRPC ingress.
type SubscriptionEventResponse struct {
	noStreamHint
	id               string
	payload          []byte
	encoder          subEventEncoder
	responseHeaders  http.Header
	responseTrailers map[string][]string
}

// NewSubscriptionEventResponse is the bare-payload event (result-only
// consumers: the gRPC ingress, the emerald server).
func NewSubscriptionEventResponse(id string, payload []byte) *SubscriptionEventResponse {
	return &SubscriptionEventResponse{id: id, payload: payload, encoder: rawEventEncoder{}}
}

// NewJsonRpcSubscriptionEventResponse is the event wrapped in a JSON-RPC
// notification envelope referencing the client subscription id (the WS server).
func NewJsonRpcSubscriptionEventResponse(id, method string, payload []byte, subId json.RawMessage) *SubscriptionEventResponse {
	return &SubscriptionEventResponse{
		id:      id,
		payload: payload,
		encoder: jsonRpcEventEncoder{method: method, subId: subId},
	}
}

func (s *SubscriptionEventResponse) ResponseResult() []byte {
	return s.payload
}

func (s *SubscriptionEventResponse) ResponseResultString() (string, error) {
	return "", nil
}

func (s *SubscriptionEventResponse) ResponseCode() int {
	return 0
}

func (s *SubscriptionEventResponse) GetError() *ResponseError {
	return nil
}

// EncodeResponse ignores realId: an event references the subscription id, not
// the request id.
func (s *SubscriptionEventResponse) EncodeResponse(_ []byte) io.Reader {
	return s.encoder.Encode(s.payload)
}

func (s *SubscriptionEventResponse) HasError() bool {
	return false
}

func (s *SubscriptionEventResponse) HasStream() bool {
	return false
}

func (s *SubscriptionEventResponse) Id() string {
	return s.id
}

func (s *SubscriptionEventResponse) IsEnd() bool {
	return false
}

func (s *SubscriptionEventResponse) ResponseHeaders() http.Header {
	return s.responseHeaders
}

func (s *SubscriptionEventResponse) WithResponseHeaders(headers http.Header) *SubscriptionEventResponse {
	s.responseHeaders = headers
	return s
}

func (s *SubscriptionEventResponse) ResponseTrailers() map[string][]string {
	return s.responseTrailers
}

func (s *SubscriptionEventResponse) WithResponseTrailers(trailers map[string][]string) *SubscriptionEventResponse {
	s.responseTrailers = trailers
	return s
}

// SubscriptionEndResponse is the final frame of a stream that completed
// cleanly (a bounded gRPC stream). It carries no payload - only the upstream
// trailers the ingress must deliver with the OK status.
type SubscriptionEndResponse struct {
	noStreamHint
	id               string
	responseHeaders  http.Header
	responseTrailers map[string][]string
}

func NewSubscriptionEndResponse(id string) *SubscriptionEndResponse {
	return &SubscriptionEndResponse{id: id}
}

func (s *SubscriptionEndResponse) ResponseResult() []byte {
	return nil
}

func (s *SubscriptionEndResponse) ResponseResultString() (string, error) {
	return "", nil
}

func (s *SubscriptionEndResponse) ResponseCode() int {
	return 0
}

func (s *SubscriptionEndResponse) GetError() *ResponseError {
	return nil
}

func (s *SubscriptionEndResponse) EncodeResponse(_ []byte) io.Reader {
	return bytes.NewReader(nil)
}

func (s *SubscriptionEndResponse) HasError() bool {
	return false
}

func (s *SubscriptionEndResponse) HasStream() bool {
	return false
}

func (s *SubscriptionEndResponse) Id() string {
	return s.id
}

func (s *SubscriptionEndResponse) IsEnd() bool {
	return true
}

func (s *SubscriptionEndResponse) ResponseHeaders() http.Header {
	return s.responseHeaders
}

func (s *SubscriptionEndResponse) WithResponseHeaders(headers http.Header) *SubscriptionEndResponse {
	s.responseHeaders = headers
	return s
}

func (s *SubscriptionEndResponse) ResponseTrailers() map[string][]string {
	return s.responseTrailers
}

func (s *SubscriptionEndResponse) WithResponseTrailers(trailers map[string][]string) *SubscriptionEndResponse {
	s.responseTrailers = trailers
	return s
}

// subEventEncoder turns an event payload into the bytes the client receives.
type subEventEncoder interface {
	Encode(payload []byte) io.Reader
}

// rawEventEncoder delivers the payload as is.
type rawEventEncoder struct{}

func (rawEventEncoder) Encode(payload []byte) io.Reader {
	return bytes.NewReader(payload)
}

// jsonRpcEventEncoder wraps the payload in a JSON-RPC notification envelope:
// {"jsonrpc":"2.0","method":<method>,"params":{"result":<payload>,"subscription":<subId>}}.
type jsonRpcEventEncoder struct {
	method string
	subId  json.RawMessage
}

type jsonRpcWsSubResponse struct {
	JsonRpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  jsonRpcWsParams `json:"params"`
}

func (e jsonRpcEventEncoder) Encode(payload []byte) io.Reader {
	resp := jsonRpcWsSubResponse{
		JsonRpc: "2.0",
		Method:  e.method,
		Params: jsonRpcWsParams{
			Result:       payload,
			Subscription: e.subId,
		},
	}
	respBytes, err := sonic.Marshal(resp)
	if err != nil {
		return iotest.ErrReader(err)
	}
	return bytes.NewReader(respBytes)
}

var _ SubscriptionResponseHolder = (*SubscriptionEventResponse)(nil)
var _ SubscriptionResponseHolder = (*SubscriptionEndResponse)(nil)
var _ subEventEncoder = rawEventEncoder{}
var _ subEventEncoder = jsonRpcEventEncoder{}
