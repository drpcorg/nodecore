package http_server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

type RequestHandler interface {
	RequestDecode(context.Context) (*Request, error)
	ResponseEncode(response protocol.ResponseHolder) *Response
	IsSingle() bool
	RequestCount() int
	GetRequestType() protocol.RequestType
}

type RestHandler struct {
	preReq         *Request
	methodTemplate string
	requestBody    []byte
	requestParams  *protocol.RequestParams
}

// NewRestHandler builds a REST handler from an incoming HTTP request.
// restPath is the path under /queries/{chain}/ (already URL-decoded by
// echo, with the query string stripped). The query string and headers are
// carried on req so the parser can promote them into RequestParams. For
// GETs and other no-body verbs the request body is allowed to be empty;
// only non-empty bodies are validated as JSON so callers like algod's REST
// API don't get rejected at parse time.
func NewRestHandler(preReq *Request, req *http.Request, restPath string) (*RestHandler, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 && !sonic.Valid(body) {
		return nil, errors.New("no valid json")
	}
	specName := chains.GetMethodSpecNameByChainName(preReq.Chain)
	template, requestParams, err := parseRestRequest(req, restPath, specName)
	if err != nil {
		return nil, err
	}
	return &RestHandler{
		preReq:         preReq,
		methodTemplate: template,
		requestBody:    body,
		requestParams:  requestParams,
	}, nil
}

func (r *RestHandler) RequestDecode(_ context.Context) (*Request, error) {
	specName := chains.GetMethodSpecNameByChainName(r.preReq.Chain)
	upstreamReq := protocol.NewUpstreamRestRequest(
		"1",
		r.methodTemplate,
		r.requestParams,
		r.requestBody,
		specName,
	)
	return &Request{
		Chain:            r.preReq.Chain,
		UpstreamRequests: []protocol.RequestHolder{upstreamReq},
	}, nil
}

func (r *RestHandler) ResponseEncode(response protocol.ResponseHolder) *Response {
	return &Response{
		ResponseReader: response.EncodeResponse(nil),
		Order:          0,
	}
}

func (r *RestHandler) IsSingle() bool {
	return true
}

func (r *RestHandler) RequestCount() int {
	return 1
}

func (r *RestHandler) GetRequestType() protocol.RequestType {
	return protocol.Rest
}

var _ RequestHandler = (*RestHandler)(nil)

type JsonRpcHandler struct {
	preReq          *Request
	idMap           map[string]lo.Tuple2[json.RawMessage, int]
	requestBody     []byte
	single          bool
	jsonRpcRequests []protocol.JsonRpcRequestBody
	isWsCtx         bool
}

var _ RequestHandler = (*JsonRpcHandler)(nil)

func NewJsonRpcHandler(preReq *Request, requestBody io.Reader, isWsCtx bool) (*JsonRpcHandler, error) {
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	var jsonRpcRequests []protocol.JsonRpcRequestBody
	rawReq := string(bytes.TrimLeft(body, " \t\n\r"))
	if len(rawReq) > 0 {
		switch rawReq[0] {
		case '[':
			var requests []protocol.JsonRpcRequestBody
			if err := sonic.UnmarshalString(rawReq, &requests); err != nil {
				return nil, err
			}
			jsonRpcRequests = requests
		case '{':
			var request protocol.JsonRpcRequestBody
			if err := sonic.UnmarshalString(rawReq, &request); err != nil {
				return nil, err
			}
			jsonRpcRequests = []protocol.JsonRpcRequestBody{request}
		}
	} else {
		return nil, decoder.SyntaxError{}
	}

	return &JsonRpcHandler{
		isWsCtx:         isWsCtx,
		preReq:          preReq,
		requestBody:     body,
		jsonRpcRequests: jsonRpcRequests,
		idMap:           make(map[string]lo.Tuple2[json.RawMessage, int]),
		single:          len(rawReq) > 0 && rawReq[0] == '{',
	}, nil
}

func (j *JsonRpcHandler) IsSingle() bool {
	return j.single
}

func (j *JsonRpcHandler) GetRequestType() protocol.RequestType {
	return protocol.JsonRpc
}

func (j *JsonRpcHandler) RequestCount() int {
	count := 0
	for _, request := range j.jsonRpcRequests {
		if len(bytes.TrimSpace(request.Id)) > 0 {
			count++
		}
	}
	return count
}

func (j *JsonRpcHandler) RequestDecode(ctx context.Context) (*Request, error) {
	upstreamRequests := make([]protocol.RequestHolder, 0)

	responseOrder := 0
	for _, jsonRpcReq := range j.jsonRpcRequests {
		id, err := uuid.NewUUID()
		if err != nil {
			return nil, err
		}
		order := -1
		if len(bytes.TrimSpace(jsonRpcReq.Id)) > 0 {
			order = responseOrder
			responseOrder++
		}
		j.idMap[id.String()] = lo.T2(jsonRpcReq.Id, order)

		specName := chains.GetMethodSpecNameByChainName(j.preReq.Chain)
		isSub := j.isWsCtx && specs.IsSubscribeMethod(specName, jsonRpcReq.Method)

		var upstreamReq protocol.RequestHolder
		if protocol.IsStream(jsonRpcReq.Method) { // for tests
			upstreamReq = protocol.NewStreamUpstreamJsonRpcRequest(id.String(), jsonRpcReq, specName)
		} else {
			upstreamReq = protocol.NewUpstreamJsonRpcRequest(id.String(), jsonRpcReq, isSub, specName)
		}
		upstreamRequests = append(upstreamRequests, upstreamReq)
	}

	return &Request{
		Chain:            j.preReq.Chain,
		UpstreamRequests: upstreamRequests,
	}, nil
}

func (j *JsonRpcHandler) ResponseEncode(response protocol.ResponseHolder) *Response {
	realId := []byte("0")
	order := -1
	idPair, ok := j.idMap[response.Id()]
	if ok {
		realId = idPair.A
		order = idPair.B
	}
	return &Response{
		ResponseReader: response.EncodeResponse(realId),
		Order:          order,
		Suppress:       ok && len(bytes.TrimSpace(idPair.A)) == 0,
	}
}
