package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"io"
	"strconv"
	"testing/iotest"
)

type SubscriptionEventResponse struct {
	id    string
	event []byte
}

func NewSubscriptionEventResponse(id string, event []byte) *SubscriptionEventResponse {
	return &SubscriptionEventResponse{event: event, id: id}
}

func (s *SubscriptionEventResponse) ResponseResult() []byte {
	return s.event
}

func (s *SubscriptionEventResponse) GetError() *ResponseError {
	return nil
}

func (s *SubscriptionEventResponse) EncodeResponse(realId []byte) io.Reader {
	return bytes.NewReader(s.event)
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

type WsJsonRpcResponse struct {
	id     string
	result []byte
	error  *ResponseError
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

type BaseUpstreamResponse struct {
	id          string
	result      []byte
	error       *ResponseError
	requestType RequestType
	stream      io.Reader
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
		} else {
			if h.stream != nil {
				return h.stream
			} else {
				return jsonRpcResponseReader(realId, "result", h.ResponseResult())
			}
		}
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
		response = parseJsonRpcBody(id, body)
	case Rest:
		response = parseHttpResponse(id, body, responseCode)
	default:
		panic(fmt.Sprintf("not an http response type - %s", requestType))
	}
	response.requestType = requestType
	return response
}

func parseHttpResponse(id string, body []byte, responseCode int) *BaseUpstreamResponse {
	var err *ResponseError
	result := body
	if responseCode != 200 {
		err, result = parseError(body), body
	}
	return &BaseUpstreamResponse{
		id:     id,
		result: result,
		error:  err,
	}
}

func parseJsonRpcBody(id string, body []byte) *BaseUpstreamResponse {
	var upstreamError *ResponseError
	var result []byte

	searcher := astSearcher(body)

	if resultNode, err := searcher.GetByPath("result"); err == nil {
		if rawResult, err := resultNode.Raw(); err == nil {
			result = []byte(rawResult)
		}
	}
	if errorNode, err := searcher.GetByPath("error"); err == nil {
		if errorRaw, err := errorNode.Raw(); err == nil {
			bodyBytes := []byte(errorRaw)
			if errorNode.TypeSafe() == ast.V_STRING {
				upstreamError, result = ResponseErrorWithMessage(errorRaw[1:len(errorRaw)-1]), bodyBytes
			} else {
				upstreamError, result = parseError([]byte(errorRaw)), bodyBytes
			}
		}
	}

	if upstreamError == nil && len(result) == 0 {
		upstreamError = IncorrectResponseBodyError(errors.New("wrong json-rpc response - there is neither result nor error"))
	}

	return &BaseUpstreamResponse{
		id:     id,
		result: result,
		error:  upstreamError,
	}
}

func parseError(errorRaw []byte) *ResponseError {
	jsonRpcErr := jsonRpcError{}
	if err := sonic.Unmarshal(errorRaw, &jsonRpcErr); err == nil {
		message := "internal server error"
		if jsonRpcErr.Message != "" {
			message = jsonRpcErr.Message
		} else if jsonRpcErr.Error != "" {
			message = jsonRpcErr.Message
		}

		code := 500
		if jsonRpcErr.Code != nil {
			code = *jsonRpcErr.Code
		}

		return ResponseErrorWithData(code, message, jsonRpcErr.Data)
	}
	return ServerError()
}

func astSearcher(body []byte) *ast.Searcher {
	searcher := ast.NewSearcher(string(body))
	searcher.ConcurrentRead = false
	searcher.CopyReturn = false

	return searcher
}

type jsonRpcWsParams struct {
	Result       json.RawMessage `json:"result"`
	Subscription json.RawMessage `json:"subscription"`
}

type jsonRpcWsMessage struct {
	Id     string           `json:"id"`
	Result json.RawMessage  `json:"result"`
	Params *jsonRpcWsParams `json:"params"`
	Error  json.RawMessage  `json:"error"`
}

func ParseJsonRpcWsMessage(body []byte) *WsResponse {
	var id string
	var responseType = Unknown
	var subId string
	var upstreamError *ResponseError
	message := body

	wsMessage := jsonRpcWsMessage{}
	err := sonic.Unmarshal(body, &wsMessage)
	if err == nil {
		id = wsMessage.Id

		if wsMessage.Params != nil {
			responseType = Ws
			subId = ResultAsString(wsMessage.Params.Subscription)
			message = wsMessage.Params.Result
		} else {
			if len(wsMessage.Result) > 0 {
				responseType = JsonRpc
				message = wsMessage.Result
			}
			if len(wsMessage.Error) > 0 {
				responseType = JsonRpc
				message = wsMessage.Error
				upstreamError = parseError(wsMessage.Error)
			}
		}
	}

	if id == "" && subId == "" && upstreamError == nil {
		upstreamError = IncorrectResponseBodyError(errors.New("wrong json-rpc ws response"))
	}

	return &WsResponse{
		Id:      id,
		Type:    responseType,
		Message: message,
		SubId:   subId,
		Error:   upstreamError,
		Event:   body,
	}
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
}

func (j *JsonRpcWsUpstreamResponse) ResponseChan() chan *WsResponse {
	return j.messages
}

func NewJsonRpcWsUpstreamResponse(messages chan *WsResponse) *JsonRpcWsUpstreamResponse {
	return &JsonRpcWsUpstreamResponse{
		messages: messages,
	}
}

type ReplyError struct {
	id            string
	ErrorKind     ResponseErrorKind
	responseError *ResponseError
	responseType  RequestType
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
	var respErr *ResponseError
	if errors.As(err, &respErr) {
		return &ReplyError{
			id:            id,
			responseError: respErr,
			responseType:  responseType,
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
