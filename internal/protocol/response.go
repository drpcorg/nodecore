package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/bcicen/jstream"
	"github.com/bytedance/sonic"
	"io"
	"strconv"
	"testing/iotest"
)

type HttpUpstreamResponse struct {
	id           string
	result       []byte
	error        *ResponseError
	responseType RequestType
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

func (h *HttpUpstreamResponse) GetError() *ResponseError {
	return h.error
}

func (h *HttpUpstreamResponse) EncodeResponse(realId []byte) io.Reader {
	if h.responseType == JsonRpc {
		if h.HasError() {
			var errBody []byte
			if h.result != nil {
				errBody = h.result
			} else {
				errBody = []byte(h.error.Error())
			}
			return jsonRpcResponseReader(realId, "error", errBody)
		} else {
			return jsonRpcResponseReader(realId, "result", h.result)
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
	response.responseType = requestType
	return response
}

func parseHttpResponse(id string, body []byte, responseCode int) *HttpUpstreamResponse {
	var err *ResponseError
	result := body
	if responseCode != 200 {
		dec := bodyDecoder(body, 0)
		for obj := range dec.Stream() {
			err, result = readError(obj.Value)
		}
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

	dec := bodyDecoder(body, 1)
	for obj := range dec.Stream() {
		if kv, ok := obj.Value.(jstream.KV); ok {
			if kv.Key == "error" {
				upstreamError, result = readError(kv.Value)
			}
			if kv.Key == "result" {
				result, upstreamError = readResult(kv.Value)
			}
		}
	}
	if (upstreamError == nil && len(result) == 0) || dec.Err() != nil {
		upstreamError = IncorrectResponseBodyError(errors.New("wrong json-rpc response - there is neither result nor error"))
	}

	return &HttpUpstreamResponse{
		id:     id,
		result: result,
		error:  upstreamError,
	}
}

func ParseJsonRpcWsMessage(body []byte) *WsResponse {
	var id string
	var responseType = Unknown
	var subId string
	var upstreamError *ResponseError
	message := body

	dec := bodyDecoder(body, 1)
	for obj := range dec.Stream() {
		if kv, ok := obj.Value.(jstream.KV); ok {
			if kv.Key == "id" {
				id = kv.Value.(string)
			}
			if kv.Key == "params" {
				responseType = Ws
				switch params := kv.Value.(type) {
				case jstream.KVS:
					for _, paramsKv := range params {
						switch paramsKv.Key {
						case "subscription":
							subBytes, upstreamErr := readResult(paramsKv.Value)
							if upstreamErr != nil {
								upstreamError = upstreamErr
							} else {
								subId = ResultAsString(subBytes)
							}
						case "result":
							message, upstreamError = readResult(paramsKv.Value)
						}
					}
				}
			}
			if kv.Key == "result" {
				responseType = JsonRpc
				message, upstreamError = readResult(kv.Value)
			}
			if kv.Key == "error" {
				upstreamError, _ = readError(kv.Value)
				responseType = JsonRpc
			}
		}
	}

	if dec.Err() != nil {
		upstreamError = IncorrectResponseBodyError(errors.New("wrong json-rpc response from ws"))
	}

	return &WsResponse{
		Id:      id,
		Type:    responseType,
		Message: message,
		SubId:   subId,
		Error:   upstreamError,
	}
}

func bodyDecoder(body []byte, level int) *jstream.Decoder {
	return jstream.NewDecoder(bytes.NewReader(body), level).EmitKV().ObjectAsKVS()
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

func readResult(result interface{}) ([]byte, *ResponseError) {
	resBytes, err := sonic.Marshal(result)
	if err != nil {
		return nil, IncorrectResponseBodyError(err)
	}

	return resBytes, nil
}

func readError(errorBody interface{}) (*ResponseError, []byte) {
	code := 0
	message := ""
	var data interface{}

	switch errValue := errorBody.(type) {
	case jstream.KVS:
		for _, kv := range errValue {
			if kv.Key == "message" || kv.Key == "error" {
				message = kv.Value.(string)
			}
			if kv.Key == "code" {
				code = int(kv.Value.(float64))
			}
			if kv.Key == "data" {
				switch dataValue := kv.Value.(type) {
				case string:
					data = dataValue
				case jstream.KVS:
					dataValues := map[string]interface{}{}
					for _, dataKv := range dataValue {
						dataValues[dataKv.Key] = dataKv.Value
					}
					if len(dataValues) > 0 {
						data = dataValues
					}
				}
			}
		}
	case string:
		message = errValue
	}
	errorBytes, err := sonic.Marshal(errorBody)
	if err != nil {
		return IncorrectResponseBodyError(err), nil
	}

	return ResponseErrorWithData(code, message, data), errorBytes
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
	return NewReplyError(id, ServerError(err), responseType)
}

func (r *ReplyError) ResponseResult() []byte {
	return nil
}

func (r *ReplyError) GetError() *ResponseError {
	return r.responseError
}

type jsonRpcError struct {
	Code    int         `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

func (r *ReplyError) EncodeResponse(realId []byte) io.Reader {
	switch r.responseType {
	case JsonRpc:
		jsonRpcErr := jsonRpcError{
			Code:    r.responseError.Code,
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
