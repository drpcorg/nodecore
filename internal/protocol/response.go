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

type HttpUpstreamResponse struct {
	id          string
	result      []byte
	error       *ResponseError
	requestType RequestType
	stream      io.Reader
}

var _ ResponseHolder = (*HttpUpstreamResponse)(nil)

func (h *HttpUpstreamResponse) Id() string {
	return h.id
}

func (h *HttpUpstreamResponse) ResponseResult() []byte {
	if h.HasError() {
		return nil
	}
	return h.result
}

func (h *HttpUpstreamResponse) HasStream() bool {
	return h.stream != nil
}

func (h *HttpUpstreamResponse) GetError() *ResponseError {
	return h.error
}

func (h *HttpUpstreamResponse) EncodeResponse(realId []byte) io.Reader {
	if h.requestType == JsonRpc {
		if h.HasError() {
			var errBody []byte
			if h.result != nil {
				errBody = h.result
			} else {
				errBody = []byte(h.error.Error())
			}
			return jsonRpcResponseReader(realId, "error", errBody)
		} else {
			if h.stream != nil {
				return h.stream
			} else {
				return jsonRpcResponseReader(realId, "result", h.result)
			}
		}
	}
	return bytes.NewReader(h.result)
}

func (h *HttpUpstreamResponse) HasError() bool {
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

func NewHttpUpstreamResponseStream(id string, reader io.Reader, requestType RequestType) *HttpUpstreamResponse {
	return &HttpUpstreamResponse{
		id:          id,
		requestType: requestType,
		stream:      reader,
	}
}

func NewSimpleHttpUpstreamResponse(id string, body []byte, requestType RequestType) *HttpUpstreamResponse {
	return &HttpUpstreamResponse{
		id:          id,
		result:      body,
		requestType: requestType,
	}
}

func NewHttpUpstreamResponse(id string, body []byte, responseCode int, requestType RequestType) *HttpUpstreamResponse {
	var response *HttpUpstreamResponse
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

func parseHttpResponse(id string, body []byte, responseCode int) *HttpUpstreamResponse {
	var err *ResponseError
	result := body
	if responseCode != 200 {
		err, result = parseError(body), body
	}
	return &HttpUpstreamResponse{
		id:     id,
		result: result,
		error:  err,
	}
}

func parseJsonRpcBody(id string, body []byte) *HttpUpstreamResponse {
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

	return &HttpUpstreamResponse{
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

func NewHttpUpstreamResponseWithError(error *ResponseError) *HttpUpstreamResponse {
	return &HttpUpstreamResponse{
		error: error,
	}
}

type WsResponse struct {
	Id      string
	SubId   string
	Message []byte
	Type    RequestType
	Error   *ResponseError
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
	responseError *ResponseError
	responseType  RequestType
}

func NewReplyError(id string, responseError *ResponseError, responseType RequestType) *ReplyError {
	return &ReplyError{
		id:            id,
		responseError: responseError,
		responseType:  responseType,
	}
}

func NewReplyErrorFromErr(id string, err error, responseType RequestType) *ReplyError {
	var respErr *ResponseError
	if errors.As(err, &respErr) {
		return &ReplyError{
			id:            id,
			responseError: respErr,
			responseType:  responseType,
		}
	}
	return NewReplyError(id, ServerErrorWithCause(err), responseType)
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
