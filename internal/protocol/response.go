package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing/iotest"

	"github.com/bytedance/sonic"
)

// HasResponseHeaders is an optional capability for response holders that
// carry upstream HTTP response headers (e.g. QR<N>-id-* quorum signatures).
// Kept separate from ResponseHolder to avoid forcing every implementation.
type HasResponseHeaders interface {
	ResponseHeaders() http.Header
}

type SubscriptionEventResponse struct {
	id    string
	event []byte
}

type SubscriptionMessageResponse struct {
	id      string
	message []byte
}

type SubscriptionResultResponse struct {
	id     string
	result []byte
}

type SubscriptionMethodResultResponse struct {
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

type WsJsonRpcResponse struct {
	id     string
	result []byte
	error  *ResponseError
}

func (w *WsJsonRpcResponse) ResponseResultString() (string, error) {
	if len(w.result) > 0 && w.result[0] == '"' && w.result[len(w.result)-1] == '"' {
		return string(w.result[1 : len(w.result)-1]), nil
	}
	return "", errors.New("result is not a string")
}

func NewWsJsonRpcResponse(id string, result []byte, error *ResponseError) *WsJsonRpcResponse {
	return &WsJsonRpcResponse{
		id:     id,
		result: result,
		error:  error,
	}
}

func (w *WsJsonRpcResponse) ResponseResult() []byte {
	return w.result
}

func (w *WsJsonRpcResponse) GetError() *ResponseError {
	return w.error
}

func (w *WsJsonRpcResponse) EncodeResponse(realId []byte) io.Reader {
	if w.HasError() {
		return jsonRpcResponseReader(realId, "error", w.ResponseResult())
	} else {
		return jsonRpcResponseReader(realId, "result", w.ResponseResult())
	}
}

func (w *WsJsonRpcResponse) HasError() bool {
	return w.error != nil
}

func (w *WsJsonRpcResponse) HasStream() bool {
	return false
}

func (w *WsJsonRpcResponse) Id() string {
	return w.id
}

var _ ResponseHolder = (*WsJsonRpcResponse)(nil)

func (w *WsJsonRpcResponse) ResponseCode() int {
	return 0
}

type BaseUpstreamResponse struct {
	id              string
	result          []byte
	error           *ResponseError
	requestType     RequestType
	stream          io.Reader
	responseCode    int
	responseHeaders http.Header
}

func (h *BaseUpstreamResponse) ResponseCode() int {
	return h.responseCode
}

func (h *BaseUpstreamResponse) ResponseHeaders() http.Header {
	return h.responseHeaders
}

func (h *BaseUpstreamResponse) WithResponseHeaders(headers http.Header) *BaseUpstreamResponse {
	h.responseHeaders = headers
	return h
}

func (h *BaseUpstreamResponse) ResponseResultString() (string, error) {
	if len(h.result) > 0 && h.result[0] == '"' && h.result[len(h.result)-1] == '"' {
		return string(h.result[1 : len(h.result)-1]), nil
	}
	return "", errors.New("result is not a string")
}

var _ ResponseHolder = (*BaseUpstreamResponse)(nil)

func (h *BaseUpstreamResponse) Id() string {
	return h.id
}

func (h *BaseUpstreamResponse) ResponseResult() []byte {
	return h.result
}

func (h *BaseUpstreamResponse) HasStream() bool {
	return h.stream != nil
}

func (h *BaseUpstreamResponse) GetError() *ResponseError {
	return h.error
}

func (h *BaseUpstreamResponse) EncodeResponse(realId []byte) io.Reader {
	if h.requestType == JsonRpc {
		if h.HasError() {
			return jsonRpcResponseReader(realId, "error", h.ResponseResult())
		}
		if h.stream != nil {
			return h.stream
		}
		return jsonRpcResponseReader(realId, "result", h.ResponseResult())
	}
	if h.stream != nil {
		return h.stream
	}
	return bytes.NewReader(h.result)
}

func (h *BaseUpstreamResponse) HasError() bool {
	return h.error != nil
}

func jsonRpcResponseReader(id []byte, bodyName string, body []byte) io.Reader {
	return io.MultiReader(
		bytes.NewReader([]byte(`{"id":`)),
		bytes.NewReader(id),
		bytes.NewReader([]byte(fmt.Sprintf(`,"jsonrpc":"2.0","%s":`, bodyName))),
		bytes.NewReader(body),
		bytes.NewReader([]byte("}")),
	)
}

func NewHttpUpstreamResponseStream(id string, reader io.Reader, requestType RequestType) *BaseUpstreamResponse {
	return &BaseUpstreamResponse{
		id:          id,
		requestType: requestType,
		stream:      reader,
	}
}

func NewSimpleHttpUpstreamResponse(id string, body []byte, requestType RequestType) *BaseUpstreamResponse {
	return &BaseUpstreamResponse{
		id:          id,
		result:      body,
		requestType: requestType,
	}
}

func NewHttpUpstreamResponse(id string, body []byte, responseCode int, requestType RequestType) *BaseUpstreamResponse {
	var response *BaseUpstreamResponse
	switch requestType {
	case JsonRpc:
		response = parseJsonRpcBody(id, body, responseCode)
	case Rest:
		response = parseHttpResponse(id, body, responseCode)
	default:
		panic(fmt.Sprintf("not an http response type - %s", requestType))
	}
	response.requestType = requestType
	return response
}

var quote = byte('"')

func ResultAsString(result []byte) string {
	if len(result) == 0 {
		return ""
	}
	if result[0] == quote && result[len(result)-1] == quote {
		return string(result[1 : len(result)-1])
	}
	return string(result)
}

func ResultAsNumber(result []byte) uint64 {
	if len(result) == 0 {
		return 0
	}
	num, err := strconv.ParseInt(string(result), 10, 64)
	if err != nil {
		return 0
	}
	return uint64(num)
}

func NewHttpUpstreamResponseWithError(error *ResponseError) *BaseUpstreamResponse {
	return &BaseUpstreamResponse{
		error: error,
	}
}

type WsResponse struct {
	Id      string
	SubId   string
	Message []byte
	Type    RequestType
	Error   *ResponseError
	Event   []byte
}

type JsonRpcWsUpstreamResponse struct {
	messages chan *WsResponse
	subOpId  string
}

func (j *JsonRpcWsUpstreamResponse) OpId() string {
	return j.subOpId
}

func (j *JsonRpcWsUpstreamResponse) ResponseChan() chan *WsResponse {
	return j.messages
}

func NewJsonRpcWsUpstreamResponse(messages chan *WsResponse, subOpId string) *JsonRpcWsUpstreamResponse {
	return &JsonRpcWsUpstreamResponse{
		messages: messages,
		subOpId:  subOpId,
	}
}

type ReplyError struct {
	id            string
	ErrorKind     ResponseErrorKind
	responseError *ResponseError
	responseType  RequestType
}

func (r *ReplyError) ResponseCode() int {
	return 0
}

func (r *ReplyError) ResponseResultString() (string, error) {
	return "", nil
}

func NewPartialFailure(request RequestHolder, responseError *ResponseError) *ReplyError {
	return NewReplyError(
		request.Id(),
		responseError,
		request.RequestType(),
		PartialFailure,
	)
}

func NewTotalFailure(request RequestHolder, responseError *ResponseError) *ReplyError {
	return NewReplyError(
		request.Id(),
		responseError,
		request.RequestType(),
		TotalFailure,
	)
}

func NewReplyError(id string, responseError *ResponseError, responseType RequestType, errorKind ResponseErrorKind) *ReplyError {
	return &ReplyError{
		id:            id,
		responseError: responseError,
		responseType:  responseType,
		ErrorKind:     errorKind,
	}
}

func NewTotalFailureFromErr(id string, err error, responseType RequestType) *ReplyError {
	if respErr, ok := errors.AsType[*ResponseError](err); ok {
		return &ReplyError{
			id:            id,
			responseError: respErr,
			responseType:  responseType,
			ErrorKind:     TotalFailure,
		}
	}
	return NewReplyError(id, ServerErrorWithCause(err), responseType, TotalFailure)
}

func (r *ReplyError) HasStream() bool {
	return false
}

func (r *ReplyError) ResponseResult() []byte {
	return nil
}

func (r *ReplyError) GetError() *ResponseError {
	return r.responseError
}

type jsonRpcError struct {
	Message string      `json:"message,omitempty"`
	Code    *int        `json:"code,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func (r *ReplyError) EncodeResponse(realId []byte) io.Reader {
	switch r.responseType {
	case JsonRpc:
		jsonRpcErr := jsonRpcError{
			Code:    &r.responseError.Code,
			Message: r.responseError.Message,
			Data:    r.responseError.Data,
		}
		jsonRpcErrBytes, err := sonic.Marshal(jsonRpcErr)
		if err != nil {
			return iotest.ErrReader(err)
		}
		return jsonRpcResponseReader(realId, "error", jsonRpcErrBytes)
	case Rest:
		return io.MultiReader(
			bytes.NewReader([]byte("{")),
			bytes.NewReader([]byte(fmt.Sprintf(`"message":"%s"`, r.responseError.Message))),
			bytes.NewReader([]byte("}")),
		)
	default:
		return nil
	}
}

func (r *ReplyError) HasError() bool {
	return true
}

func (r *ReplyError) Id() string {
	return r.id
}

var _ ResponseHolder = (*ReplyError)(nil)
