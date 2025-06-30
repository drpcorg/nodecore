package ws

import (
	"context"
	"fmt"
	"github.com/bytedance/sonic"
	"github.com/drpcorg/dsheltie/internal/protocol"
	specs "github.com/drpcorg/dsheltie/pkg/methods"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const (
	wsReadBuffer  = 1024
	wsWriteBuffer = 1024
)

var wsBufferPool = new(sync.Pool)

type WsConnection interface {
	SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error)
	SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error)
}

type JsonRpcWsConnection struct {
	writeMutex sync.Mutex

	upId        string
	methodSpec  string
	endpoint    string
	rpcTimeout  time.Duration
	ctx         context.Context
	connectFunc func() (*websocket.Conn, error)
	connection  *utils.Atomic[*websocket.Conn]
	internalId  atomic.Uint64
	requests    *utils.CMap[string, reqOp] // to store internal ids and websocket requests
	subs        *utils.CMap[string, reqOp] // to store a subId and its request to identify events
}

func NewJsonRpcWsConnection(ctx context.Context, upId, methodSpec, endpoint string, additionalHeaders map[string]string) WsConnection {
	log.Info().Msgf("connecting to %s", endpoint)

	dialer := &websocket.Dialer{
		ReadBufferSize:  wsReadBuffer,
		WriteBufferSize: wsWriteBuffer,
		WriteBufferPool: wsBufferPool,
		Proxy:           http.ProxyFromEnvironment,
	}
	var header http.Header = map[string][]string{}
	for key, val := range additionalHeaders {
		header.Add(key, val)
	}

	connectFunc := func() (*websocket.Conn, error) {
		conn, _, err := dialer.DialContext(ctx, endpoint, header)
		if err != nil {
			return nil, err
		}
		return conn, nil
	}

	wsConnection := &JsonRpcWsConnection{
		upId:        upId,
		methodSpec:  methodSpec,
		endpoint:    endpoint,
		ctx:         ctx,
		connectFunc: connectFunc,
		requests:    utils.NewCMap[string, reqOp](),
		subs:        utils.NewCMap[string, reqOp](),
		rpcTimeout:  1 * time.Minute,
		connection:  utils.NewAtomic[*websocket.Conn](),
	}

	err := wsConnection.connect()
	if err != nil {
		go wsConnection.reconnect()
	}

	return wsConnection
}

func (w *JsonRpcWsConnection) SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	timeout := time.NewTimer(w.rpcTimeout)
	respChan, err := w.SendWsRequest(ctx, upstreamRequest)
	if err != nil {
		return nil, err
	}
	select {
	case response, ok := <-respChan:
		if !ok {
			return nil, fmt.Errorf("no response on method %s via ws", upstreamRequest.Method())
		}
		return response, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("no response on method %s via ws due to %s", upstreamRequest.Method(), ctx.Err().Error())
	case <-timeout.C:
		return nil, fmt.Errorf("no response within %v on method %s via ws", w.rpcTimeout, upstreamRequest.Method())
	}
}

func (w *JsonRpcWsConnection) SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error) {
	jsonBody, err := sonic.Get(upstreamRequest.Body())
	if err != nil {
		return nil, fmt.Errorf("invalid json-rpc request, cause %s", err.Error())
	}

	internalId := w.internalId.Add(1)
	requestId := fmt.Sprintf("%d", internalId)
	_, err = jsonBody.SetAny("id", requestId)
	if err != nil {
		return nil, fmt.Errorf("couldn't replace an id, cause %s", err.Error())
	}

	req := &reqOp{
		responseChan:     make(chan *protocol.WsResponse, 50),
		internalMessages: make(chan *protocol.WsResponse, 50),
		completed:        atomic.Bool{},
		ctx:              ctx,
		method:           upstreamRequest.Method(),
	}

	rawBody, _ := jsonBody.Raw()

	err = w.writeMessage([]byte(rawBody))
	if err != nil {
		return nil, err
	}
	w.requests.Store(requestId, req)

	go w.startProcess(req)

	return req.responseChan, nil
}

func (w *JsonRpcWsConnection) connect() error {
	conn, err := w.connectFunc()
	if err != nil {
		log.Warn().Err(err).Msgf("couldn't connect to %s, trying to reconnect", w.endpoint)
		return err
	} else {
		if w.connection.Load() != nil {
			w.completeAll()
		}

		log.Info().Msgf("connected to %s, listening to messages", w.endpoint)

		w.connection.Store(conn)
		go w.processMessages()
	}
	return nil
}

func (w *JsonRpcWsConnection) reconnect() {
	for {
		err := w.connect()
		if err == nil {
			break
		}
		time.Sleep(10 * time.Second) //TODO: refactor to exponential backoff policy
	}
}

func (r *reqOp) writeInternal(message *protocol.WsResponse) {
	r.internalMessages <- message
}

func (w *JsonRpcWsConnection) startProcess(r *reqOp) {
	for {
		select {
		case <-r.ctx.Done():
			r.completeReq()
			w.unsubscribe(r)
			return
		case message, ok := <-r.internalMessages:
			if ok {
				r.responseChan <- message
			}
		}
	}
}

func (w *JsonRpcWsConnection) processMessages() {
	for {
		_, message, err := w.connection.Load().ReadMessage()
		if err != nil {
			log.Warn().Err(err).Msgf("couldn't read message from %s, trying to reconnect", w.endpoint)
			w.reconnect()
			break
		}
		wsResponse := protocol.ParseJsonRpcWsMessage(message)
		switch wsResponse.Type {
		case protocol.JsonRpc:
			w.onRpcMessage(wsResponse)
		case protocol.Ws:
			w.onSubscriptionMessage(wsResponse)
		default:
			log.Warn().Msgf("unknown ws response format - %s", string(wsResponse.Message))
		}
	}
}

func (w *JsonRpcWsConnection) onRpcMessage(response *protocol.WsResponse) {
	if req, ok := w.requests.Load(response.Id); ok {
		defer w.requests.Delete(response.Id)

		if req.completed.Load() {
			return
		}

		req.writeInternal(response)
		if response.Error == nil && specs.IsSubscribeMethod(w.methodSpec, req.method) {
			req.subId = protocol.ResultAsString(response.Message)
			w.subs.Store(req.subId, req)
		}
	}
}

func (w *JsonRpcWsConnection) onSubscriptionMessage(response *protocol.WsResponse) {
	if req, ok := w.subs.Load(response.SubId); ok {
		if req.completed.Load() {
			w.subs.Delete(response.SubId)
			return
		}
		req.writeInternal(response)
	}
}

func (w *JsonRpcWsConnection) writeMessage(message []byte) error {
	w.writeMutex.Lock()
	defer w.writeMutex.Unlock()

	if w.connection.Load() == nil {
		return fmt.Errorf("no connection to %s", w.endpoint)
	}

	err := w.connection.Load().WriteMessage(websocket.TextMessage, message)
	if err != nil {
		return err
	}

	return nil
}

func (w *JsonRpcWsConnection) completeAll() {
	err := w.connection.Load().Close()
	if err != nil {
		log.Warn().Err(err).Msg("couldn't close a ws connection")
	}
	w.requests.Range(func(key string, val *reqOp) bool {
		w.requests.Delete(key)
		return true
	})
	w.subs.Range(func(key string, val *reqOp) bool {
		w.subs.Delete(key)
		val.completeReq()
		return true
	})
}

func (w *JsonRpcWsConnection) unsubscribe(op *reqOp) {
	if op.subId != "" {
		if unsubMethod, ok := specs.GetUnsubscribeMethod(w.methodSpec, op.method); ok {
			params := []interface{}{op.subId}
			unsubReq, err := protocol.NewInternalJsonRpcUpstreamRequest(unsubMethod, params)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse unsubscribe method %s and subId %s", unsubMethod, op.subId)
			} else {
				err = w.writeMessage(unsubReq.Body())
				if err != nil {
					log.Warn().Err(err).Msgf("couldn't unsubscribe with method %s and subId %s", unsubMethod, op.subId)
				} else {
					log.Info().Msgf("sub %s of upstream %s has been successfully stopped", op.subId, w.upId)
				}
			}
		}
	}
}

func (r *reqOp) completeReq() {
	if !r.completed.Load() {
		r.completed.Store(true)
		go func() {
			time.Sleep(100 * time.Millisecond)
			close(r.responseChan)
		}()
	}
}

type reqOp struct {
	responseChan     chan *protocol.WsResponse
	internalMessages chan *protocol.WsResponse
	completed        atomic.Bool
	ctx              context.Context
	method           string
	subId            string
}
