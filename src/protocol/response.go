package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/bcicen/jstream"
	"github.com/bytedance/sonic"
	"io"
)

type ResponseType int

const (
	Rest ResponseType = iota
	JsonRpc
	Ws
	Unknown
)

type HttpUpstreamResponse struct {
	id     interface{}
	result []byte
	error  *UpstreamError
}

func (h *HttpUpstreamResponse) Id() interface{} {
	return h.id
}

func (h *HttpUpstreamResponse) ResponseResult() []byte {
	return h.result
}

func (h *HttpUpstreamResponse) ResponseError() *UpstreamError {
	return h.error
}

func (h *HttpUpstreamResponse) EncodeResponse() io.Reader {
	return nil
}

func (h *HttpUpstreamResponse) HasError() bool {
	return h.error != nil
}

func NewHttpUpstreamResponse(id interface{}, body []byte, responseCode int, responseType ResponseType) *HttpUpstreamResponse {
	switch responseType {
	case JsonRpc:
		return parseJsonRpcBody(id, body)
	case Rest:
		return parseHttpResponse(id, body, responseCode)
	default:
		return nil
	}
}

func parseHttpResponse(id interface{}, body []byte, responseCode int) *HttpUpstreamResponse {
	var err *UpstreamError
	if responseCode != 200 {
		dec := bodyDecoder(body, 0)
		for obj := range dec.Stream() {
			err = readError(obj.Value)
		}
	}
	return &HttpUpstreamResponse{
		id:     id,
		result: body,
		error:  err,
	}
}

func parseJsonRpcBody(id interface{}, body []byte) *HttpUpstreamResponse {
	var upstreamError *UpstreamError
	var result []byte

	dec := bodyDecoder(body, 1)
	for obj := range dec.Stream() {
		if kv, ok := obj.Value.(jstream.KV); ok {
			if kv.Key == "error" {
				upstreamError = readError(kv.Value)
			}
			if kv.Key == "result" {
				result, upstreamError = readResult(kv.Value)
			}
		}
	}
	if upstreamError == nil && len(result) == 0 {
		upstreamError = NewIncorrectResponseBodyError(errors.New("wrong json-rpc response - there is neither result nor error"))
	}

	return &HttpUpstreamResponse{
		id:     id,
		result: result,
		error:  upstreamError,
	}
}

func ParseJsonRpcWsMessage(body []byte) *WsResponse {
	var id uint64
	var responseType = Unknown
	var subId string
	var upstreamError *UpstreamError
	message := body

	dec := bodyDecoder(body, 1)
	for obj := range dec.Stream() {
		if kv, ok := obj.Value.(jstream.KV); ok {
			if kv.Key == "id" {
				id = uint64(kv.Value.(float64))
			}
			if kv.Key == "params" {
				responseType = Ws
				switch params := kv.Value.(type) {
				case map[string]interface{}:
					subId = fmt.Sprintf("%v", params["subscription"])
					if res, paramsOk := params["result"]; paramsOk {
						message, upstreamError = readResult(res)
					}
				}
			}
			if kv.Key == "result" {
				responseType = JsonRpc
				message, upstreamError = readResult(kv.Value)
			}
			if kv.Key == "error" {
				upstreamError = readError(kv.Value)
				responseType = JsonRpc
			}
		}
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
	return jstream.NewDecoder(bytes.NewReader(body), level).EmitKV()
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

func readResult(result interface{}) ([]byte, *UpstreamError) {
	resBytes, err := sonic.Marshal(result)
	if err != nil {
		return nil, NewIncorrectResponseBodyError(err)
	}

	return resBytes, nil
}

func readError(errorBody interface{}) *UpstreamError {
	code := 0
	message := ""
	var data interface{}

	switch errValue := errorBody.(type) {
	case string:
		message = errValue
	case map[string]interface{}:
		if errStr, errStrOk := errValue["message"]; errStrOk {
			message = errStr.(string)
		} else {
			if errStr, errStrOk = errValue["error"]; errStrOk {
				message = errStr.(string)
			}
		}
		if errCode, errCodeOk := errValue["code"]; errCodeOk {
			code = int(errCode.(float64))
		}
		if errData, errDataOk := errValue["data"]; errDataOk {
			data = errData
		}
	}

	return NewUpstreamErrorWithData(code, message, data)
}

func NewHttpUpstreamResponseWithError(error *UpstreamError) *HttpUpstreamResponse {
	return &HttpUpstreamResponse{
		error: error,
	}
}

type WsResponse struct {
	Id      uint64
	SubId   string
	Message []byte
	Type    ResponseType
	Error   *UpstreamError
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
