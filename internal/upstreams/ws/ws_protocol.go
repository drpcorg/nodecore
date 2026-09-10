package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
)

type WsProtocol interface {
	RequestFrame(request protocol.RequestHolder) (*RequestFrame, error)
	ParseWsMessage(payload []byte) (*protocol.WsResponse, error)

	DoOnCloseFunc(writeRequestFunc WriteRequest) DoOnClose
}

const initialInternalId = 100

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
	if _, err = jsonBody.SetAny("id", nextId); err != nil {
		return nil, fmt.Errorf("couldn't replace an id, cause - %s", err.Error())
	}

	rawBody, _ := jsonBody.Raw()

	subType, err := getSubscription(&jsonBody, request)
	if err != nil {
		return nil, fmt.Errorf("couldn't get a subscription type, cause - %w", err)
	}

	return NewRequestFrame(requestId, subType, []byte(rawBody)), nil
}

func (j *JsonRpcWsProtocol) DoOnCloseFunc(writeRequestFunc WriteRequest) DoOnClose {
	return func(op RequestOperation) {
		subId := op.SubIdBytes()
		if len(subId) == 0 {
			return
		}
		unsubMethod, ok := specs.GetUnsubscribeMethod(j.methodSpec, op.Method())
		if !ok {
			return
		}

		params := []interface{}{json.RawMessage(subId)}
		unsubReq, err := protocol.NewInternalUpstreamJsonRpcRequest(unsubMethod, params, j.chain)
		if err != nil {
			log.Error().Err(err).Msgf("couldn't parse unsubscribe method %s and subId %s", unsubMethod, subId)
			return
		}
		body, err := unsubReq.Body()
		if err != nil {
			log.Error().Err(err).Msgf("couldn't get a body of method %s and subId %s", unsubMethod, subId)
			return
		}
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

func (j *JsonRpcWsProtocol) ParseWsMessage(payload []byte) (*protocol.WsResponse, error) {
	wsResponse := protocol.ParseJsonRpcWsMessage(payload)
	if wsResponse.Type != protocol.JsonRpc && wsResponse.Type != protocol.Ws {
		return nil, fmt.Errorf("invalid response type - %s", wsResponse.Type)
	}
	return wsResponse, nil
}

func NewJsonRpcWsProtocol(upstreamId, methodSpec string, chain chains.Chain) *JsonRpcWsProtocol {
	wsProtocol := &JsonRpcWsProtocol{
		upstreamId: upstreamId,
		methodSpec: methodSpec,
		internalId: atomic.Int64{},
		chain:      chain,
	}
	wsProtocol.internalId.Store(initialInternalId)

	return wsProtocol
}

// errNonUtf8SubType rejects an eth_subscribe subscription type that is not valid
// UTF-8. It becomes the "subscription" label of json_ws_connections, and
// WithLabelValues panics on invalid UTF-8; nothing in this process recovers,
// so it would crash nodecore (same class as execution_flow.go:262).
var errNonUtf8SubType = errors.New("subscription type is not a valid utf-8 string")

func getSubscription(jsonBody *ast.Node, request protocol.RequestHolder) (string, error) {
	if !request.IsSubscribe() {
		return "", nil
	}
	if request.Method() == "eth_subscribe" {
		ethSubType := jsonBody.GetByPath("params", 0)
		// The type guard keeps the slice below off non-string nodes: Raw() on the
		// number 1 returns "1", and sub[1:len(sub)-1] would panic. The length check
		// is a second guard so the slice never depends on that invariant holding
		// elsewhere. A non-string first param is not a rejection reason - it falls
		// through to the method name and the upstream decides.
		if ethSubType != nil && ethSubType.TypeSafe() == ast.V_STRING {
			if sub, err := ethSubType.Raw(); err == nil && len(sub) >= 2 {
				subType := sub[1 : len(sub)-1]
				// This value becomes a Prometheus label; WithLabelValues panics on an
				// invalid one, and nothing recovers, so it would crash nodecore.
				if !utf8.ValidString(subType) {
					return "", errNonUtf8SubType
				}
				return subType, nil
			}
		}
	}
	return request.Method(), nil
}

var _ WsProtocol = (*JsonRpcWsProtocol)(nil)
