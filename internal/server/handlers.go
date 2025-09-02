package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/pkg/chains"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
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
	preReq      *Request
	requestBody []byte
	method      string
}

func NewRestHandler(preReq *Request, method string, requestBody io.Reader) (*RestHandler, error) {
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	if !sonic.Valid(body) {
		return nil, errors.New("no valid json")
	}
	return &RestHandler{
		preReq:      preReq,
		method:      method,
		requestBody: body,
	}, nil
}

func (r *RestHandler) RequestDecode(ctx context.Context) (*Request, error) {
	upstreamReq := protocol.NewUpstreamRestRequest()
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
	return len(j.jsonRpcRequests)
}

func (j *JsonRpcHandler) RequestDecode(ctx context.Context) (*Request, error) {
	upstreamRequests := make([]protocol.RequestHolder, 0)

	for i, jsonRpcReq := range j.jsonRpcRequests {
		id, err := uuid.NewUUID()
		if err != nil {
			return nil, err
		}
		j.idMap[id.String()] = lo.T2(jsonRpcReq.Id, i)

		isSub := j.isWsCtx && specs.IsSubscribeMethod(chains.GetMethodSpecNameByChainName(j.preReq.Chain), jsonRpcReq.Method)
		specMethod := specs.GetSpecMethod(chains.GetMethodSpecNameByChainName(j.preReq.Chain), jsonRpcReq.Method)

		var upstreamReq protocol.RequestHolder
		if protocol.IsStream(jsonRpcReq.Method) { // for tests
			upstreamReq = protocol.NewStreamUpstreamJsonRpcRequest(id.String(), jsonRpcReq.Id, jsonRpcReq.Method, jsonRpcReq.Params, specMethod)
		} else {
			upstreamReq = protocol.NewUpstreamJsonRpcRequest(id.String(), jsonRpcReq.Id, jsonRpcReq.Method, jsonRpcReq.Params, isSub, specMethod)
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
	}
}
