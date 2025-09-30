package ws

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var jsonRpcWsConnectionsMetric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: config.AppName,
	Subsystem: "request",
	Name:      "json_ws_connections",
	Help:      "The current number of active JSON-RPC subscriptions",
}, []string{"chain", "upstream", "subscription"})

var jsonRpcWsOperations = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: config.AppName,
	Subsystem: "request",
	Name:      "json_ws_operations",
}, []string{"chain", "upstream"})

func init() {
	prometheus.MustRegister(jsonRpcWsConnectionsMetric)
}

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
	chain       chains.Chain
	methodSpec  string
	endpoint    string
	connClosed  atomic.Bool
	rpcTimeout  time.Duration
	ctx         context.Context
	connectFunc func() (*websocket.Conn, error)
	connection  *utils.Atomic[*websocket.Conn]
	internalId  atomic.Uint64
	requests    *utils.CMap[string, reqOp] // to store internal ids and websocket requests
	subs        *utils.CMap[string, reqOp] // to store a subId and its request to identify events
	executor    failsafe.Executor[bool]
}

func NewJsonRpcWsConnection(
	ctx context.Context,
	chain chains.Chain,
	upId,
	methodSpec,
	endpoint string,
	additionalHeaders map[string]string,
) WsConnection {
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
		chain:       chain,
		methodSpec:  methodSpec,
		endpoint:    endpoint,
		ctx:         ctx,
		connectFunc: connectFunc,
		requests:    utils.NewCMap[string, reqOp](),
		subs:        utils.NewCMap[string, reqOp](),
		rpcTimeout:  1 * time.Minute,
		connection:  utils.NewAtomic[*websocket.Conn](),
		executor:    failsafe.NewExecutor[bool](createConnectionRetryPolicy(endpoint)),
		connClosed:  atomic.Bool{},
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
	body, err := upstreamRequest.Body()
	if err != nil {
		return nil, fmt.Errorf("couldn't parse a request body, cause - %s", err.Error())
	}
	jsonBody, err := sonic.Get(body)
	if err != nil {
		return nil, fmt.Errorf("invalid json-rpc request, cause - %s", err.Error())
	}

	internalId := w.internalId.Add(1)
	requestId := fmt.Sprintf("%d", internalId)
	_, err = jsonBody.SetAny("id", requestId)
	if err != nil {
		return nil, fmt.Errorf("couldn't replace an id, cause - %s", err.Error())
	}

	rawBody, _ := jsonBody.Raw()

	err = w.writeMessage([]byte(rawBody))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	cancelFunc := utils.NewAtomic[context.CancelFunc]()
	cancelFunc.Store(cancel)

	method := utils.NewAtomic[string]()
	method.Store(upstreamRequest.Method())
	subType := utils.NewAtomic[string]()
	subType.Store(getSubscription(&jsonBody, upstreamRequest))
	subId := utils.NewAtomic[string]()
	subId.Store("")

	req := &reqOp{
		responseChan:     make(chan *protocol.WsResponse, 50),
		internalMessages: make(chan *protocol.WsResponse, 50),
		completed:        atomic.Bool{},
		ctx:              ctx,
		cancel:           cancelFunc,
		method:           method,
		subType:          subType,
		subId:            subId,
	}

	w.requests.Store(requestId, req)

	go w.startProcess(req)

	return req.responseChan, nil
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
		w.connClosed.Store(false)

		w.connection.Store(conn)
		go w.processMessages()
	}
	return nil
}

func (w *JsonRpcWsConnection) reconnect() {
	_, _ = w.executor.GetWithExecution(func(exec failsafe.Execution[bool]) (bool, error) {
		err := w.connect()
		if err != nil {
			return false, err
		}

		return true, nil
	})
}

func (r *reqOp) writeInternal(message *protocol.WsResponse) {
	r.internalMessages <- message
}

func (w *JsonRpcWsConnection) startProcess(r *reqOp) {
	jsonRpcWsOperations.WithLabelValues(w.chain.String(), w.upId).Inc()
	defer jsonRpcWsOperations.WithLabelValues(w.chain.String(), w.upId).Dec()
	for {
		select {
		case <-r.ctx.Done():
			r.completeReq()
			if !w.connClosed.Load() {
				w.unsubscribe(r)
			}
			if r.subId.Load() != "" {
				w.subs.Delete(r.subId.Load())
				jsonRpcWsConnectionsMetric.WithLabelValues(w.chain.String(), w.upId, r.subType.Load()).Dec()
			}
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
			log.Warn().Msgf("unknown ws response format - %s, all ws operations should be stopped", string(wsResponse.Message))
			w.reconnect()
			break
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
		if response.Error == nil && specs.IsSubscribeMethod(w.methodSpec, req.method.Load()) {
			req.subId.Store(protocol.ResultAsString(response.Message))
			w.subs.Store(req.subId.Load(), req)
			if req.subId.Load() != "" {
				jsonRpcWsConnectionsMetric.WithLabelValues(w.chain.String(), w.upId, req.subType.Load()).Inc()
			}
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
	w.connClosed.Store(true)
	if err != nil {
		log.Warn().Err(err).Msg("couldn't close a ws connection")
	}
	w.requests.Range(func(key string, val *reqOp) bool {
		w.requests.Delete(key)
		val.cancel.Load()()
		return true
	})
	w.subs.Range(func(key string, val *reqOp) bool {
		w.subs.Delete(key)
		val.cancel.Load()()
		return true
	})
}

func (w *JsonRpcWsConnection) unsubscribe(op *reqOp) {
	subId := op.subId.Load()
	if subId != "" {
		if unsubMethod, ok := specs.GetUnsubscribeMethod(w.methodSpec, op.method.Load()); ok {
			params := []interface{}{subId}
			unsubReq, err := protocol.NewInternalUpstreamJsonRpcRequest(unsubMethod, params)
			if err != nil {
				log.Warn().Err(err).Msgf("couldn't parse unsubscribe method %s and subId %s", unsubMethod, subId)
			} else {
				body, err := unsubReq.Body()
				if err != nil {
					log.Warn().Err(err).Msgf("couldn't get a body of method %s and subId %s", unsubMethod, subId)
				} else {
					err = w.writeMessage(body)
					if err != nil {
						log.Warn().Err(err).Msgf("couldn't unsubscribe with method %s of upstream %s and subId %s", unsubMethod, w.upId, subId)
					} else {
						log.Info().Msgf("sub %s of upstream %s has been successfully stopped", subId, w.upId)
					}
				}
			}
		}
	}
}

func (r *reqOp) completeReq() {
	if r.completed.CompareAndSwap(false, true) {
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
	cancel           *utils.Atomic[context.CancelFunc]
	method           *utils.Atomic[string]
	subId            *utils.Atomic[string]
	subType          *utils.Atomic[string]
}

func createConnectionRetryPolicy(url string) failsafe.Policy[bool] {
	retryPolicy := retrypolicy.Builder[bool]()

	retryPolicy.WithMaxAttempts(-1) // endless retries
	retryPolicy.WithBackoff(1*time.Second, 60*time.Second)
	retryPolicy.WithJitter(3 * time.Second)

	retryPolicy.HandleIf(func(result bool, err error) bool {
		return !result
	})

	retryPolicy.OnRetry(func(event failsafe.ExecutionEvent[bool]) {
		log.Warn().Msgf("attemtring to reconnect to %s", url)
	})

	return retryPolicy.Build()
}
