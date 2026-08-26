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
//   - SubscriptionMessageResponse: the JSON-RPC subscribe ack carrying the
//     client subscription id (not an event frame);
//   - SubscriptionMethodResultResponse: a JSON-RPC notification envelope;
//   - SubscriptionResultResponse / SubscriptionEventResponse: bare event
//     payloads (result-only consumers);
//   - SubscriptionEndResponse: the clean end of a bounded stream (not an event
//     frame; carries the upstream trailers).

type SubscriptionEventResponse struct {
	noStreamHint
	id    string
	event []byte
}

type SubscriptionMessageResponse struct {
	noStreamHint
	id      string
	message []byte
}

// SubscriptionEndResponse is the final, non-event frame of a stream that
// completed cleanly (a bounded gRPC stream). It carries no payload - only the
// upstream trailers the ingress must deliver with the OK status.
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

func (s *SubscriptionEndResponse) IsEventFrame() bool {
	return false
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

var _ SubscriptionResponseHolder = (*SubscriptionEndResponse)(nil)

type SubscriptionResultResponse struct {
	noStreamHint
	id               string
	result           []byte
	responseHeaders  http.Header
	responseTrailers map[string][]string
}

func (s *SubscriptionResultResponse) ResponseHeaders() http.Header {
	return s.responseHeaders
}

func (s *SubscriptionResultResponse) WithResponseHeaders(headers http.Header) *SubscriptionResultResponse {
	s.responseHeaders = headers
	return s
}

func (s *SubscriptionResultResponse) ResponseTrailers() map[string][]string {
	return s.responseTrailers
}

func (s *SubscriptionResultResponse) WithResponseTrailers(trailers map[string][]string) *SubscriptionResultResponse {
	s.responseTrailers = trailers
	return s
}

type SubscriptionMethodResultResponse struct {
	noStreamHint
	id     string
	method string
	result []byte
	subId  json.RawMessage
}

func NewSubscriptionMethodResultResponse(id, method string, result []byte, subId json.RawMessage) *SubscriptionMethodResultResponse {
	return &SubscriptionMethodResultResponse{
		id:     id,
		method: method,
		result: result,
		subId:  subId,
	}
}

func (s *SubscriptionMethodResultResponse) ResponseResult() []byte {
	return s.result
}

func (s *SubscriptionMethodResultResponse) ResponseResultString() (string, error) {
	return "", nil
}

func (s *SubscriptionMethodResultResponse) ResponseCode() int {
	return 0
}

func (s *SubscriptionMethodResultResponse) GetError() *ResponseError {
	return nil
}

type jsonRpcWsSubResponse struct {
	JsonRpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  jsonRpcWsParams `json:"params"`
}

func (s *SubscriptionMethodResultResponse) EncodeResponse(_ []byte) io.Reader {
	resp := jsonRpcWsSubResponse{
		JsonRpc: "2.0",
		Method:  s.method,
		Params: jsonRpcWsParams{
			Result:       s.result,
			Subscription: s.subId,
		},
	}
	respBytes, err := sonic.Marshal(resp)
	if err != nil {
		return iotest.ErrReader(err)
	}
	return bytes.NewReader(respBytes)
}

func (s *SubscriptionMethodResultResponse) HasError() bool {
	return false
}

func (s *SubscriptionMethodResultResponse) HasStream() bool {
	return false
}

func (s *SubscriptionMethodResultResponse) Id() string {
	return s.id
}

func (s *SubscriptionMethodResultResponse) IsEventFrame() bool {
	return true
}

func (s *SubscriptionEventResponse) ResponseResultString() (string, error) {
	return "", nil
}

func (s *SubscriptionMessageResponse) ResponseResultString() (string, error) {
	return "", nil
}

func (s *SubscriptionResultResponse) ResponseResultString() (string, error) {
	return "", nil
}

func NewSubscriptionMessageEventResponse(id string, message []byte) *SubscriptionMessageResponse {
	return &SubscriptionMessageResponse{message: message, id: id}
}

func NewSubscriptionEventResponse(id string, event []byte) *SubscriptionEventResponse {
	return &SubscriptionEventResponse{event: event, id: id}
}

func NewSubscriptionResultEventResponse(id string, result []byte) *SubscriptionResultResponse {
	return &SubscriptionResultResponse{result: result, id: id}
}

func (s *SubscriptionEventResponse) IsEventFrame() bool {
	return true
}

func (s *SubscriptionMessageResponse) IsEventFrame() bool {
	return false
}

func (s *SubscriptionResultResponse) IsEventFrame() bool {
	return true
}

func (s *SubscriptionEventResponse) ResponseResult() []byte {
	return s.event
}

func (s *SubscriptionMessageResponse) ResponseResult() []byte {
	return s.message
}

func (s *SubscriptionResultResponse) ResponseResult() []byte {
	return s.result
}

func (s *SubscriptionEventResponse) GetError() *ResponseError {
	return nil
}

func (s *SubscriptionMessageResponse) GetError() *ResponseError {
	return nil
}

func (s *SubscriptionResultResponse) GetError() *ResponseError {
	return nil
}

func (s *SubscriptionEventResponse) EncodeResponse(realId []byte) io.Reader {
	return bytes.NewReader(s.event)
}

func (s *SubscriptionMessageResponse) EncodeResponse(realId []byte) io.Reader {
	return jsonRpcResponseReader(realId, "result", s.message)
}

func (s *SubscriptionResultResponse) EncodeResponse(realId []byte) io.Reader {
	return bytes.NewReader(s.result)
}

func (s *SubscriptionEventResponse) HasError() bool {
	return false
}

func (s *SubscriptionMessageResponse) HasError() bool {
	return false
}

func (s *SubscriptionResultResponse) HasError() bool {
	return false
}

func (s *SubscriptionEventResponse) HasStream() bool {
	return false
}

func (s *SubscriptionMessageResponse) HasStream() bool {
	return false
}

func (s *SubscriptionResultResponse) HasStream() bool {
	return false
}

func (s *SubscriptionEventResponse) Id() string {
	return s.id
}

func (s *SubscriptionMessageResponse) Id() string {
	return s.id
}

func (s *SubscriptionResultResponse) Id() string {
	return s.id
}

func (s *SubscriptionEventResponse) ResponseCode() int {
	return 0
}

func (s *SubscriptionMessageResponse) ResponseCode() int {
	return 0
}

func (s *SubscriptionResultResponse) ResponseCode() int {
	return 0
}

var _ SubscriptionResponseHolder = (*SubscriptionEventResponse)(nil)
var _ SubscriptionResponseHolder = (*SubscriptionMessageResponse)(nil)
var _ SubscriptionResponseHolder = (*SubscriptionResultResponse)(nil)
var _ SubscriptionResponseHolder = (*SubscriptionMethodResultResponse)(nil)
