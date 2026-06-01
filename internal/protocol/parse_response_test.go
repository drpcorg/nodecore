package protocol_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// incorrectBodyErr is the canonical "we got something we couldn't parse"
// shape both the JSON-RPC envelope parser and the WS message parser
// emit on irrecoverable inputs. Pinned here so all the parse-failure
// tests can assert the full error in one go.
func incorrectBodyErr(reason string) *protocol.ResponseError {
	return &protocol.ResponseError{
		Code:    -32001,
		Message: "incorrect response body: " + reason,
		Data:    nil,
	}
}

// ============================================================
// parseJsonRpcBody (via NewHttpUpstreamResponse(..., JsonRpc))
// ============================================================

func TestJsonRpcResponse_ResultObjectIsExtracted(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","result":{"number":"0x11"}}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	assert.False(t, resp.HasError())
	assert.Nil(t, resp.GetError(), "successful responses must carry no error")
	assert.JSONEq(t, `{"number":"0x11"}`, string(resp.ResponseResult()))
	assert.Equal(t, 200, resp.ResponseCode())
}

// JSON-RPC permits a null result - it must not be mistaken for "no result".
func TestJsonRpcResponse_NullResultIsValid(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","result":null}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	assert.False(t, resp.HasError())
	assert.Nil(t, resp.GetError())
	assert.Equal(t, "null", string(resp.ResponseResult()))
}

// Error encoded as a string (not an object) is a common dproxy-flavour
// quirk - the parser must accept it and unwrap the quotes. The parser
// uses ResponseErrorWithMessage which only sets Message; Code and Data
// stay zero/nil.
func TestJsonRpcResponse_StringErrorIsAccepted(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","error":"upstream blew up"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    0,
		Message: "upstream blew up",
		Data:    nil,
	}, resp.GetError())
}

// If both fields are present, error takes precedence - the result
// extraction runs first but the error pass overwrites both fields. Pinning
// this so a refactor of the parser order doesn't quietly flip semantics.
func TestJsonRpcResponse_BothResultAndError_ErrorWins(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","result":"ok","error":{"code":-7,"message":"sketchy"}}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    -7,
		Message: "sketchy",
		Data:    nil,
	}, resp.GetError())
}

// HTTP status passes through verbatim - parseJsonRpcBody doesn't decide
// error-ness from the status code, only from the JSON envelope.
func TestJsonRpcResponse_HttpStatusCodePropagates(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","error":{"code":-1,"message":"x"}}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 500, protocol.JsonRpc)

	assert.Equal(t, 500, resp.ResponseCode(),
		"HTTP status must be carried on the response holder independent of the JSON-RPC envelope")
	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    -1,
		Message: "x",
		Data:    nil,
	}, resp.GetError(),
		"the envelope error must be fully preserved even when the HTTP status is non-2xx")
}

// Neither result nor error means the upstream sent something we can't
// understand - surface a deterministic error rather than a silent empty
// result.
func TestJsonRpcResponse_NeitherResultNorErrorIsParseFailure(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 200, protocol.JsonRpc)

	require.True(t, resp.HasError())
	assert.Equal(t, incorrectBodyErr("wrong json-rpc response - there is neither result nor error"),
		resp.GetError())
}

func TestJsonRpcResponse_NonJsonBodyIsParseFailure(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponse("1", []byte("not-json-at-all"), 200, protocol.JsonRpc)

	require.True(t, resp.HasError())
	assert.Equal(t, incorrectBodyErr("wrong json-rpc response - there is neither result nor error"),
		resp.GetError())
}

// ============================================================
// REST tier
// ============================================================

// jsonRpcError exposes both Message and Error fields; if only Error is set
// in the structured body, tryParseStructuredError must use it as the
// human-facing message. Code is absent in the body so the HTTP status wins.
func TestRestResponse_StructuredErrorFieldUsedAsMessage(t *testing.T) {
	body := []byte(`{"error":"BAD_REQUEST: missing token"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 400, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    400,
		Message: "BAD_REQUEST: missing token",
		Data:    nil,
	}, resp.GetError())
}

// Explicit "code": 0 in the body counts as missing and falls back to the
// HTTP status. Without this the client would see a meaningless 0 error code.
func TestRestResponse_ExplicitZeroCodeFallsBackToHttpStatus(t *testing.T) {
	body := []byte(`{"code":0,"message":"weird upstream"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 502, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    502,
		Message: "weird upstream",
		Data:    nil,
	}, resp.GetError())
}

// Whitespace-only body is functionally empty - the parser collapses it to
// the placeholder rather than letting "   " become the error message.
func TestRestResponse_WhitespaceOnlyBodyGetsPlaceholder(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponse("1", []byte("   \n\t  "), 500, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    500,
		Message: "upstream returned an empty error body",
		Data:    nil,
	}, resp.GetError())
}

// Boundary check: exactly at restErrorMessageMaxLen (512) is NOT truncated.
func TestRestResponse_MessageExactlyAtCapNotTruncated(t *testing.T) {
	body := []byte(strings.Repeat("a", 512))
	resp := protocol.NewHttpUpstreamResponse("1", body, 500, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    500,
		Message: string(body),
		Data:    nil,
	}, resp.GetError(),
		"exactly-at-cap messages must pass through verbatim with no truncation marker")
}

// Just over the cap (513) gets the truncation marker.
func TestRestResponse_MessageJustOverCapTruncated(t *testing.T) {
	body := []byte(strings.Repeat("a", 513))
	resp := protocol.NewHttpUpstreamResponse("1", body, 500, protocol.Rest)

	require.True(t, resp.HasError())
	expected := strings.Repeat("a", 512) + "... (truncated)"
	assert.Equal(t, &protocol.ResponseError{
		Code:    500,
		Message: expected,
		Data:    nil,
	}, resp.GetError(),
		"the message must be the first 512 chars plus the explicit truncation marker")
}

// JSON array (not object) on an error body shouldn't be misread as
// structured - it falls through to the text path.
func TestRestResponse_JsonArrayBodyFallsThroughToTextPath(t *testing.T) {
	body := []byte(`["a","b","c"]`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 502, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    502,
		Message: string(body),
		Data:    nil,
	}, resp.GetError())
}

// Malformed JSON object on an error body falls through to the text path
// (and so retains the literal bytes as the message).
func TestRestResponse_MalformedJsonObjectFallsThroughToTextPath(t *testing.T) {
	body := []byte(`{"code":400,`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 400, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    400,
		Message: string(body),
		Data:    nil,
	}, resp.GetError())
}

// Object with code AND data, no message - the placeholder kicks in and
// data still rides along for callers that inspect it.
func TestRestResponse_StructuredCodeOnlyGetsPlaceholderAndKeepsData(t *testing.T) {
	body := []byte(`{"code":418,"data":"context"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 418, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    418,
		Message: "upstream returned an error without a message",
		Data:    "context",
	}, resp.GetError(),
		"missing message must yield the deterministic placeholder while preserving Data")
}

// Object with both code and a full data payload of object shape.
func TestRestResponse_StructuredFullErrorIsCarriedVerbatim(t *testing.T) {
	body := []byte(`{"code":-32000,"message":"reverted","data":{"reason":"out of gas"}}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 400, protocol.Rest)

	require.True(t, resp.HasError())
	assert.Equal(t, &protocol.ResponseError{
		Code:    -32000,
		Message: "reverted",
		Data:    map[string]interface{}{"reason": "out of gas"},
	}, resp.GetError(),
		"all three fields must survive the parse - including a nested Data object")
}

// ============================================================
// ParseJsonRpcWsMessage
// ============================================================

func TestParseJsonRpcWsMessage_ResultResponse(t *testing.T) {
	body := []byte(`{"id":"42","jsonrpc":"2.0","result":"0xdeadbeef"}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, ws.Error, "result frames must carry no error")
	assert.Equal(t, "42", ws.Id)
	assert.Equal(t, protocol.JsonRpc, ws.Type,
		"top-level result frames are JSON-RPC, not subscription events")
	assert.Empty(t, ws.SubId)
	assert.Equal(t, `"0xdeadbeef"`, string(ws.Message))
}

func TestParseJsonRpcWsMessage_ErrorResponse(t *testing.T) {
	body := []byte(`{"id":"42","jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"}}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	require.NotNil(t, ws.Error)
	assert.Equal(t, &protocol.ResponseError{
		Code:    -32601,
		Message: "method not found",
		Data:    nil,
	}, ws.Error)
	assert.Equal(t, protocol.JsonRpc, ws.Type)
	assert.Equal(t, "42", ws.Id)
}

// Error response with a Data payload - all three fields must reach the
// caller so error-aware tooling can inspect the detail.
func TestParseJsonRpcWsMessage_ErrorResponseWithData(t *testing.T) {
	body := []byte(`{"id":"42","jsonrpc":"2.0","error":{"code":-7,"message":"reverted","data":"0xrevertdata"}}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	require.NotNil(t, ws.Error)
	assert.Equal(t, &protocol.ResponseError{
		Code:    -7,
		Message: "reverted",
		Data:    "0xrevertdata",
	}, ws.Error)
}

// Bodies that don't decode at all and carry no id, subId, or error must
// surface a deterministic parse failure - silently dropping them would
// poison the subscription stream.
func TestParseJsonRpcWsMessage_GarbageBodyIsParseFailure(t *testing.T) {
	ws := protocol.ParseJsonRpcWsMessage([]byte("not-json"))

	require.NotNil(t, ws.Error)
	assert.Equal(t, incorrectBodyErr("wrong json-rpc ws response"), ws.Error)
}

// JSON that decodes but contains no useful fields - same parse-failure
// surfacing as the garbage case.
func TestParseJsonRpcWsMessage_EmptyEnvelopeIsParseFailure(t *testing.T) {
	ws := protocol.ParseJsonRpcWsMessage([]byte(`{}`))

	require.NotNil(t, ws.Error)
	assert.Equal(t, incorrectBodyErr("wrong json-rpc ws response"), ws.Error)
}

// Event frames carry no id, but a subId from params.subscription. Confirm
// that the parser doesn't trip the "no id, no subId" parse-failure branch
// when a real subscription event arrives.
func TestParseJsonRpcWsMessage_SubscriptionEventHasNoErrorAndCarriesSubId(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0xabc","result":{"number":"0x1"}}}`)
	ws := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, ws.Error, "subscription events must not be flagged as parse failures")
	assert.Equal(t, protocol.Ws, ws.Type)
	assert.Equal(t, "0xabc", ws.SubId)
	assert.JSONEq(t, `{"number":"0x1"}`, string(ws.Message))
	assert.Equal(t, body, ws.Event,
		"the original full envelope must be retained as Event for downstream re-emission")
}

func TestParseWsSubMessage(t *testing.T) {
	body := []byte(`{"id":"1","jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "1", wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"`, string(wsResponse.Message))
}

func TestParseWsNumberSubMessage(t *testing.T) {
	body := []byte(`{"id":"12","jsonrpc":"2.0","result": 233242423}`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "12", wsResponse.Id)
	assert.Equal(t, protocol.JsonRpc, wsResponse.Type)
	assert.Empty(t, wsResponse.SubId)
	assert.Equal(t, `233242423`, string(wsResponse.Message))
}

func TestParseWsEvent(t *testing.T) {
	body := []byte(`{"id":"15","jsonrpc":"2.0","params": { "result": {"key":"value"}, "subscription": "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "15", wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}

func TestParseWsEventWithNumSub(t *testing.T) {
	body := []byte(`{"id":"15","jsonrpc":"2.0","params": { "result": {"key":"value"}, "subscription": 1223} }`)
	wsResponse := protocol.ParseJsonRpcWsMessage(body)

	assert.Nil(t, wsResponse.Error)
	assert.Equal(t, "15", wsResponse.Id)
	assert.Equal(t, protocol.Ws, wsResponse.Type)
	assert.Equal(t, "1223", wsResponse.SubId)
	assert.Equal(t, []byte(`{"key":"value"}`), wsResponse.Message)
}

func TestEncodeJsonRpcRequest(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		id       []byte
		expected []byte
		hasError bool
	}{
		{
			name:     "string result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`),
			id:       []byte(`25`),
			expected: []byte(`{"id":25,"jsonrpc":"2.0","result":"0x89d9f8cd1e113f4b65c1e22f3847d3672cf5761f"}`),
		},
		{
			name:     "bool result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":true}`),
			id:       []byte(`"test"`),
			expected: []byte(`{"id":"test","jsonrpc":"2.0","result":true}`),
		},
		{
			name:     "number result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":12234}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":12234}`),
		},
		{
			name:     "object result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":{"key":"value"}}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":{"key":"value"}}`),
		},
		{
			name:     "array result",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","result":[{"key":"value"}]}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","result":[{"key":"value"}]}`),
		},
		{
			name:     "error response",
			body:     []byte(`{"id":"1","jsonrpc":"2.0","error":{"message":"error","code":2}}`),
			id:       []byte(`"23r23"`),
			expected: []byte(`{"id":"23r23","jsonrpc":"2.0","error":{"message":"error","code":2}}`),
			hasError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			response := protocol.NewHttpUpstreamResponse("1", test.body, 200, protocol.JsonRpc)

			respReader := response.EncodeResponse(test.id)
			respBytes, err := io.ReadAll(respReader)

			assert.Nil(te, err)
			assert.False(te, response.HasStream())
			assert.Equal(te, "1", response.Id())
			assert.Equal(te, test.hasError, response.HasError())
			assert.Equal(te, test.expected, respBytes)
		})
	}
}

func TestEncodeReplyErrorJsonRpc(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.JsonRpc, protocol.TotalFailure)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.True(t, replyError.HasError())
	assert.Equal(t, "1", replyError.Id())
	assert.False(t, replyError.HasStream())
	assert.Nil(t, replyError.ResponseResult())
	assert.Equal(t, protocol.ServerErrorWithCause(errors.New("err cause")), replyError.GetError())
	assert.Equal(t, []byte(`{"id":55,"jsonrpc":"2.0","error":{"message":"internal server error: err cause","code":500}}`), respBytes)
}

func TestEncodeReplyErrorRest(t *testing.T) {
	replyError := protocol.NewReplyError("1", protocol.ServerErrorWithCause(errors.New("err cause")), protocol.Rest, protocol.TotalFailure)

	respReader := replyError.EncodeResponse([]byte("55"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.Equal(t, "1", replyError.Id())
	assert.True(t, replyError.HasError())
	assert.False(t, replyError.HasStream())
	assert.Nil(t, replyError.ResponseResult())
	assert.Equal(t, protocol.ServerErrorWithCause(errors.New("err cause")), replyError.GetError())
	assert.Equal(t, []byte(`{"message":"internal server error: err cause"}`), respBytes)
}

func TestEncodeWsJsonRpcResponse(t *testing.T) {
	response := protocol.NewWsJsonRpcResponse("2", []byte("result"), nil)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Nil(t, response.GetError())
	assert.Equal(t, []byte("result"), response.ResponseResult())
	assert.Equal(t, []byte(`{"id":32,"jsonrpc":"2.0","result":result}`), respBytes)
}

func TestEncodeWsJsonRpcResponseWithError(t *testing.T) {
	response := protocol.NewWsJsonRpcResponse("2", []byte("error"), protocol.ServerError())

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.True(t, response.HasError())
	assert.False(t, response.HasStream())
	assert.Equal(t, "2", response.Id())
	assert.Equal(t, []byte("error"), response.ResponseResult())
	assert.Equal(t, protocol.ServerError(), response.GetError())
	assert.Equal(t, []byte(`{"id":32,"jsonrpc":"2.0","error":error}`), respBytes)
}

func TestEncodeSubscriptionEventResponse(t *testing.T) {
	result := []byte("event")
	response := protocol.NewSubscriptionEventResponse("11", result)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t, "11", response.Id())
	assert.Nil(t, response.GetError())
	assert.False(t, response.HasStream())
	assert.Equal(t, result, response.ResponseResult())
	assert.Equal(t, result, respBytes)
}

func TestEncodeSubscriptionWithRealIdEventResponse(t *testing.T) {
	result := []byte("event")
	response := protocol.NewSubscriptionMessageEventResponse("11", result)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t, "11", response.Id())
	assert.Nil(t, response.GetError())
	assert.False(t, response.HasStream())
	assert.Equal(t, result, response.ResponseResult())
	assert.Equal(t, []byte(`{"id":32,"jsonrpc":"2.0","result":event}`), respBytes)
}

func TestEncodeSubscriptionResultEventResponse(t *testing.T) {
	result := []byte(`{"foo":"bar"}`)
	response := protocol.NewSubscriptionResultEventResponse("11", result)

	respReader := response.EncodeResponse([]byte("32"))
	respBytes, err := io.ReadAll(respReader)

	assert.Nil(t, err)
	assert.False(t, response.HasError())
	assert.Equal(t, "11", response.Id())
	assert.Nil(t, response.GetError())
	assert.False(t, response.HasStream())
	assert.True(t, response.IsEventFrame())
	assert.Equal(t, result, response.ResponseResult())
	assert.Equal(t, result, respBytes)
}

func TestRestResponseSuccessLeavesErrorNil(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponse("1", []byte(`{"ok":true}`), 200, protocol.Rest)

	assert.False(t, resp.HasError())
	assert.Nil(t, resp.GetError())
	assert.Equal(t, 200, resp.ResponseCode())
	assert.Equal(t, []byte(`{"ok":true}`), resp.ResponseResult())
}

// 201/204 etc are still success - the old != 200 check was too strict.
func TestRestResponseAcceptsAllTwoHundredCodes(t *testing.T) {
	for _, code := range []int{200, 201, 202, 204, 299} {
		resp := protocol.NewHttpUpstreamResponse("1", nil, code, protocol.Rest)
		assert.False(t, resp.HasError(), "code %d must be treated as success", code)
	}
}

// {"code":N,"message":"..."} is the canonical shape - keep using the
// upstream's own code and message verbatim.
func TestRestResponseUsesStructuredErrorBody(t *testing.T) {
	body := []byte(`{"code":400,"message":"BAD_REQUEST: missing Content-Type header"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 400, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 400, resp.GetError().Code)
	assert.Equal(t, "BAD_REQUEST: missing Content-Type header", resp.GetError().Message)
}

// Structured body without "code" - fall back to the HTTP status so callers
// always see a meaningful number.
func TestRestResponseStructuredBodyMissingCodeFallsBackToHttpStatus(t *testing.T) {
	body := []byte(`{"message":"not found"}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 404, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 404, resp.GetError().Code,
		"missing code in body must fall back to the actual HTTP status")
	assert.Equal(t, "not found", resp.GetError().Message)
}

// Plain text response (no JSON) - HTTP code becomes the error code, body
// becomes the message verbatim (within the truncation limit).
func TestRestResponseTextBodyUsesHttpStatusAndBodyAsMessage(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponse("1", []byte("Service Unavailable"), 503, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 503, resp.GetError().Code)
	assert.Equal(t, "Service Unavailable", resp.GetError().Message)
}

// HTML 404 from a misconfigured upstream - the literal body would blow up
// logs, so it gets truncated with an explicit marker.
func TestRestResponseTruncatesLongNonStructuredBody(t *testing.T) {
	huge := make([]byte, 4096)
	for i := range huge {
		huge[i] = 'a'
	}
	resp := protocol.NewHttpUpstreamResponse("1", huge, 502, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 502, resp.GetError().Code)
	assert.Contains(t, resp.GetError().Message, "... (truncated)",
		"oversized bodies must be truncated so they don't poison logs")
	assert.Less(t, len(resp.GetError().Message), len(huge),
		"truncated message must be shorter than the original body")
}

// Empty body on a 5xx still needs to produce a usable error.
func TestRestResponseEmptyBodyGetsPlaceholder(t *testing.T) {
	resp := protocol.NewHttpUpstreamResponse("1", nil, 500, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 500, resp.GetError().Code)
	assert.NotEmpty(t, resp.GetError().Message)
}

// JSON object that isn't an error envelope (no code, no message, no error)
// shouldn't be misread as structured - it falls through to the text path.
func TestRestResponseNonErrorJsonObjectFallsThroughToTextPath(t *testing.T) {
	body := []byte(`{"data":[1,2,3]}`)
	resp := protocol.NewHttpUpstreamResponse("1", body, 422, protocol.Rest)

	require.NotNil(t, resp.GetError())
	assert.Equal(t, 422, resp.GetError().Code)
	assert.Equal(t, string(body), resp.GetError().Message)
}
