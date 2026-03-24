package ws

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/rs/zerolog/log"
)

type WsProtocol interface {
	RequestFrame(request protocol.RequestHolder) (*RequestFrame, error)
	DoOnCloseFunc(writeRequestFunc WriteRequest) DoOnClose
	ParseWsMessage(payload []byte) (*protocol.WsResponse, error)
}

type JsonRpcWsProtocol struct {
	upstreamId string
	methodSpec string
	internalId atomic.Int64
	chain      chains.Chain
}

func (j *JsonRpcWsProtocol) RequestFrame(request protocol.RequestHolder) (*RequestFrame, error) {
	body, err := request.Body()
	if err != nil {
		return nil, fmt.Errorf("couldn't parse a request body, cause - %s", err.Error())
	}

	jsonBody, err := sonic.Get(body)
	if err != nil {
		return nil, fmt.Errorf("invalid json-rpc request, cause - %s", err.Error())
	}
	nextId := j.internalId.Add(1)

	requestId := fmt.Sprintf("%d", nextId)
	if _, err = jsonBody.SetAny("id", requestId); err != nil {
		return nil, fmt.Errorf("couldn't replace an id, cause - %s", err.Error())
	}

	rawBody, _ := jsonBody.Raw()

	return NewRequestFrame(requestId, getSubscription(&jsonBody, request), []byte(rawBody)), nil
}

func (j *JsonRpcWsProtocol) DoOnCloseFunc(writeRequestFunc WriteRequest) DoOnClose {
	return func(op RequestOperation) {
		subId := op.SubID()
		if subId != "" {
			if unsubMethod, ok := specs.GetUnsubscribeMethod(j.methodSpec, op.Method()); ok {
				params := []interface{}{subId}
				unsubReq, err := protocol.NewInternalUpstreamJsonRpcRequest(unsubMethod, params, j.chain)
				if err != nil {
					log.Error().Err(err).Msgf("couldn't parse unsubscribe method %s and subId %s", unsubMethod, subId)
				} else {
					body, err := unsubReq.Body()
					if err != nil {
						log.Error().Err(err).Msgf("couldn't get a body of method %s and subId %s", unsubMethod, subId)
					} else {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						err = writeRequestFunc(ctx, body)
						cancel()
						if err != nil {
							log.Error().Err(err).Msgf("couldn't unsubscribe with method %s of upstream %s and subId %s", unsubMethod, j.upstreamId, subId)
						} else {
							log.Info().Msgf("sub %s of upstream %s has been successfully stopped", subId, j.upstreamId)
						}
					}
				}
			}
		}
	}
}

func (j *JsonRpcWsProtocol) ParseWsMessage(payload []byte) (*protocol.WsResponse, error) {
	wsResponse := protocol.ParseJsonRpcWsMessage(payload)
	if wsResponse.Type != protocol.JsonRpc && wsResponse.Type != protocol.Ws {
		return nil, fmt.Errorf("invalid response type - %s", wsResponse.Type)
	}
	return wsResponse, nil
}

func NewJsonRpcWsProtocol(upstreamId, methodSpec string, chain chains.Chain) *JsonRpcWsProtocol {
	return &JsonRpcWsProtocol{
		upstreamId: upstreamId,
		methodSpec: methodSpec,
		internalId: atomic.Int64{},
		chain:      chain,
	}
}

func getSubscription(jsonBody *ast.Node, request protocol.RequestHolder) string {
	if !request.IsSubscribe() {
		return ""
	}
	if request.Method() == "eth_subscribe" {
		ethSubType := jsonBody.GetByPath("params", 0)
		if ethSubType != nil {
			sub, err := ethSubType.Raw()
			if err == nil {
				return sub[1 : len(sub)-1]
			}
		}
	}
	return request.Method()
}

var _ WsProtocol = (*JsonRpcWsProtocol)(nil)
