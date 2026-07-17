package flow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
)

// rippleNodeHealthErrors are the XRPL error codes that mean the node itself
// cannot serve the request right now (not synced, overloaded, amendment
// blocked) - the same request may well succeed on another upstream. Every
// other XRPL error code is deterministic for the request and must pass
// through untouched.
var rippleNodeHealthErrors = map[string]struct{}{
	"noNetwork":        {},
	"noCurrent":        {},
	"noClosed":         {},
	"tooBusy":          {},
	"amendmentBlocked": {},
	"notReady":         {},
	"failedToForward":  {},
}

// rippleNodeHealthMessagePrefix is matched by a retryable-errors pattern in
// pkg/errors_config/errors.yaml, which is what makes the converted response
// retryable for the flow retry policy (protocol.IsRetryable is message-driven).
const rippleNodeHealthMessagePrefix = "xrpl node-health error"

// rippleErrorNormalizer handles rippled's error convention: application errors
// are HTTP 200 with {"result": {..., "status": "error"}} and no top-level
// JSON-RPC error, so the standard parser classifies them as success. The error
// shape is uniform across all rippled methods, hence a spec-wide wildcard
// translator. Node-health errors are converted into real retryable error
// responses so the flow fails over to another upstream; deterministic request
// errors (unknownCmd, invalidParams, lgrNotFound, ...) keep the native XRPL
// wire shape untouched - clients (xrpl.js) parse it themselves.
type rippleErrorNormalizer struct{}

type rippleResultEnvelope struct {
	Status       string `json:"status"`
	Error        string `json:"error"`
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

func (t *rippleErrorNormalizer) TranslateRequest(_ context.Context, request protocol.RequestHolder) (protocol.RequestHolder, error) {
	return request, nil
}

func (t *rippleErrorNormalizer) TranslateResponse(request, _ protocol.RequestHolder, _ uint64, response protocol.ResponseHolder) protocol.ResponseHolder {
	if response.HasError() {
		return response
	}
	var result rippleResultEnvelope
	if err := sonic.Unmarshal(response.ResponseResult(), &result); err != nil || result.Status != "error" {
		return response
	}
	if _, nodeHealth := rippleNodeHealthErrors[result.Error]; !nodeHealth {
		return response
	}
	message := fmt.Sprintf("%s %s (error_code %d): %s", rippleNodeHealthMessagePrefix, result.Error, result.ErrorCode, result.ErrorMessage)
	body, err := sonic.Marshal(rippleJsonRpcErrorBody{
		Jsonrpc: "2.0",
		Id:      rippleClientRequestId(request),
		Error:   rippleJsonRpcErrorField{Code: protocol.InternalServerErrorCode, Message: message},
	})
	if err != nil {
		return protocol.NewTotalFailureFromErr(request.Id(), protocol.IncorrectResponseBodyError(err), protocol.JsonRpc)
	}
	return protocol.NewHttpUpstreamResponse(request.Id(), body, response.ResponseCode(), protocol.JsonRpc)
}

type rippleJsonRpcErrorField struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rippleJsonRpcErrorBody struct {
	Jsonrpc string                  `json:"jsonrpc"`
	Id      json.RawMessage         `json:"id"`
	Error   rippleJsonRpcErrorField `json:"error"`
}

func rippleClientRequestId(request protocol.RequestHolder) json.RawMessage {
	if body, err := jsonRpcBodyOf(request); err == nil && len(body.Id) > 0 {
		return body.Id
	}
	return json.RawMessage("null")
}
