package protocol

import (
	"bytes"
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

// HasResponseTrailers is an optional capability for response holders that
// carry upstream gRPC trailer metadata. Only the gRPC ingress consumes it -
// a gRPC client must receive trailers *as* trailers, not folded into the
// initial metadata. Keys follow gRPC metadata convention (lowercase).
type HasResponseTrailers interface {
	ResponseTrailers() map[string][]string
}

// noStreamHint is embedded by response types that never carry a streaming
// result hint (subscriptions, ws, errors); it satisfies the GetStreamHint part
// of ResponseHolder with a nil hint.
type noStreamHint struct{}

func (noStreamHint) GetStreamHint() StreamHint { return nil }

type WsJsonRpcResponse struct {
	noStreamHint
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

type GenericUpstreamResponse struct {
	id               string
	result           []byte
	error            *ResponseError
	requestType      RequestType
	stream           io.Reader
	responseCode     int
	responseHeaders  http.Header
	responseTrailers map[string][]string
	// streamHint carries the single-pass first-chunk analysis (see
	// AnalyzeFirstChunk) for a streaming response, so the gRPC result-unwrap
	// consumer can emit the "result" value without re-scanning the chunk. It is
	// nil when there is no hint (e.g. a REST stream); the HTTP consumer ignores
	// it and streams the whole envelope.
	streamHint StreamHint
}

func (h *GenericUpstreamResponse) ResponseCode() int {
	return h.responseCode
}

func (h *GenericUpstreamResponse) ResponseHeaders() http.Header {
	return h.responseHeaders
}

func (h *GenericUpstreamResponse) WithResponseHeaders(headers http.Header) *GenericUpstreamResponse {
	h.responseHeaders = headers
	return h
}

func (h *GenericUpstreamResponse) ResponseTrailers() map[string][]string {
	return h.responseTrailers
}

func (h *GenericUpstreamResponse) WithResponseTrailers(trailers map[string][]string) *GenericUpstreamResponse {
	h.responseTrailers = trailers
	return h
}

func (h *GenericUpstreamResponse) ResponseResultString() (string, error) {
	if len(h.result) > 0 && h.result[0] == '"' && h.result[len(h.result)-1] == '"' {
		return string(h.result[1 : len(h.result)-1]), nil
	}
	return "", errors.New("result is not a string")
}

var _ ResponseHolder = (*GenericUpstreamResponse)(nil)

func (h *GenericUpstreamResponse) Id() string {
	return h.id
}

func (h *GenericUpstreamResponse) ResponseResult() []byte {
	return h.result
}

func (h *GenericUpstreamResponse) HasStream() bool {
	return h.stream != nil
}

func (h *GenericUpstreamResponse) GetError() *ResponseError {
	return h.error
}

func (h *GenericUpstreamResponse) EncodeResponse(realId []byte) io.Reader {
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

func (h *GenericUpstreamResponse) HasError() bool {
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

func NewHttpUpstreamResponseStream(id string, reader io.Reader, requestType RequestType) *GenericUpstreamResponse {
	return &GenericUpstreamResponse{
		id:          id,
		requestType: requestType,
		stream:      reader,
	}
}

// WithStreamHint attaches the first-chunk analysis produced by
// AnalyzeFirstChunk so the gRPC result-unwrap consumer can emit the result
// without re-scanning the chunk. A nil hint leaves the response without one.
func (h *GenericUpstreamResponse) WithStreamHint(hint StreamHint) *GenericUpstreamResponse {
	h.streamHint = hint
	return h
}

// GetStreamHint returns the streaming hint, or nil if none was recorded.
func (h *GenericUpstreamResponse) GetStreamHint() StreamHint {
	return h.streamHint
}

func NewSimpleHttpUpstreamResponse(id string, body []byte, requestType RequestType) *GenericUpstreamResponse {
	return &GenericUpstreamResponse{
		id:          id,
		result:      body,
		requestType: requestType,
	}
}

func NewHttpUpstreamResponse(id string, body []byte, responseCode int, requestType RequestType) *GenericUpstreamResponse {
	var response *GenericUpstreamResponse
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

func NewHttpUpstreamResponseWithError(error *ResponseError) *GenericUpstreamResponse {
	return &GenericUpstreamResponse{
		error: error,
	}
}

type JsonRpcWsUpstreamResponse struct {
	messages chan SubResponse
	subOpId  string
}

func (j *JsonRpcWsUpstreamResponse) OpId() string {
	return j.subOpId
}

func (j *JsonRpcWsUpstreamResponse) ResponseChan() chan SubResponse {
	return j.messages
}

func NewJsonRpcWsUpstreamResponse(messages chan SubResponse, subOpId string) *JsonRpcWsUpstreamResponse {
	return &JsonRpcWsUpstreamResponse{
		messages: messages,
		subOpId:  subOpId,
	}
}

type ReplyError struct {
	noStreamHint
	id            string
	ErrorKind     ResponseErrorKind
	responseError *ResponseError
	responseType  RequestType
	// upstream response metadata may ride on error replies too - e.g. a
	// RESOURCE_EXHAUSTED carrying rate-limit hints in its trailers
	responseHeaders  http.Header
	responseTrailers map[string][]string
}

func (r *ReplyError) ResponseHeaders() http.Header {
	return r.responseHeaders
}

func (r *ReplyError) WithResponseHeaders(headers http.Header) *ReplyError {
	r.responseHeaders = headers
	return r
}

func (r *ReplyError) ResponseTrailers() map[string][]string {
	return r.responseTrailers
}

func (r *ReplyError) WithResponseTrailers(trailers map[string][]string) *ReplyError {
	r.responseTrailers = trailers
	return r
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
