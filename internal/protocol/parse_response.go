package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
)

func astSearcher(body []byte) *ast.Searcher {
	searcher := ast.NewSearcher(string(body))
	searcher.ConcurrentRead = false
	searcher.CopyReturn = false

	return searcher
}

func parseJsonRpcBody(id string, body []byte, responseCode int) *BaseUpstreamResponse {
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
				upstreamError, result = parseJsonRpcError([]byte(errorRaw)), bodyBytes
			}
		}
	}

	if upstreamError == nil && len(result) == 0 {
		upstreamError, result = incorrectJsonRpcBody()
	}

	return &BaseUpstreamResponse{
		id:           id,
		result:       result,
		responseCode: responseCode,
		error:        upstreamError,
	}
}

func incorrectJsonRpcBody() (*ResponseError, []byte) {
	err := IncorrectResponseBodyError(errors.New("wrong json-rpc response - there is neither result nor error"))
	jsonRpcErr := jsonRpcError{Message: err.Message, Code: new(err.Code)}
	errBytes, _ := sonic.Marshal(jsonRpcErr)
	return err, errBytes
}

func parseHttpResponse(id string, body []byte, responseCode int) *BaseUpstreamResponse {
	var err *ResponseError
	if responseCode < 200 || responseCode >= 300 {
		err = parseRestErrorBody(body, responseCode)
	}
	return &BaseUpstreamResponse{
		id:           id,
		result:       body,
		error:        err,
		responseCode: responseCode,
	}
}

// parseRestErrorBody turns a non-2xx REST response into a ResponseError.
//
//   - Body matches the conventional {"code":N,"message":"..."} shape -
//     use those fields. If "code" is absent we fall back to the HTTP
//     status code so callers always see a meaningful number.
//   - Otherwise (raw text, HTML, malformed JSON, empty body) - use the
//     HTTP status code as the error code and a truncated view of the body
//     as the message.
//
// We never fold the raw body into Message verbatim - some upstreams reply
// with multi-kilobyte HTML on 5xx and that would blow up our logs.
func parseRestErrorBody(body []byte, responseCode int) *ResponseError {
	if structured, ok := tryParseStructuredError(body); ok {
		if structured.Code == 0 {
			structured.Code = responseCode
		}
		return structured
	}
	return &ResponseError{
		Code:    responseCode,
		Message: truncateRestErrorMessage(body),
	}
}

// restErrorMessageMaxLen caps how much of a non-structured error body we
// fold into ResponseError.Message. Some upstreams reply with multi-kilobyte
// HTML on 4xx/5xx; keeping the message bounded keeps logs readable and the
// error payload returnable to the client.
const restErrorMessageMaxLen = 512

// tryParseStructuredError recognises the {"code":N,"message":"..."} shape
// that most JSON REST APIs use for error responses. Returns ok=false for
// anything else - plain text, HTML, raw JSON strings/arrays, or JSON
// objects that don't carry at least one of code/message/error.
func tryParseStructuredError(body []byte) (*ResponseError, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var parsed jsonRpcError
	if err := sonic.Unmarshal(trimmed, &parsed); err != nil {
		return nil, false
	}
	if parsed.Code == nil && parsed.Message == "" && parsed.Error == "" {
		return nil, false
	}
	msg := parsed.Message
	if msg == "" {
		msg = parsed.Error
	}
	if msg == "" {
		msg = "upstream returned an error without a message"
	}
	out := &ResponseError{Message: msg, Data: parsed.Data}
	if parsed.Code != nil {
		out.Code = *parsed.Code
	}
	return out, true
}

// truncateRestErrorMessage is the fallback message builder when the body
// isn't structured JSON. Whitespace is collapsed off the ends; anything
// longer than restErrorMessageMaxLen is cut with an explicit marker so
// readers know they're not seeing the whole payload.
func truncateRestErrorMessage(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "upstream returned an empty error body"
	}
	if len(s) > restErrorMessageMaxLen {
		return s[:restErrorMessageMaxLen] + "... (truncated)"
	}
	return s
}

// parseJsonRpcError unmarshals an upstream JSON-RPC error object into a
// ResponseError. Used both for the standard json-rpc error envelope on
// HTTP responses and for the error frames coming over JSON-RPC websocket.
// REST has its own path - see parseRestErrorBody.
func parseJsonRpcError(errorRaw []byte) *ResponseError {
	jsonRpcErr := jsonRpcError{}
	if err := sonic.Unmarshal(errorRaw, &jsonRpcErr); err == nil {
		message := "internal server error"
		if jsonRpcErr.Message != "" {
			message = jsonRpcErr.Message
		} else if jsonRpcErr.Error != "" {
			message = jsonRpcErr.Error
		}

		code := -32000
		if jsonRpcErr.Code != nil {
			code = *jsonRpcErr.Code
		}

		return ResponseErrorWithData(code, message, jsonRpcErr.Data)
	}
	return ServerError()
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
				upstreamError = parseJsonRpcError(wsMessage.Error)
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
