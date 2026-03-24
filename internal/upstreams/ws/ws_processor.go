package ws

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

type WsProcessor interface {
	utils.Lifecycle

	SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error)
	SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error)

	SubscribeWsStates(name string) *utils.Subscription[protocol.SubscribeConnectorState]
}

const rpcTimeout = 1 * time.Minute

type BaseWsProcessor struct {
	lifecycle *utils.BaseLifecycle

	wsConnDialFunc  DialFunc
	wsSession       WsSession
	wsProtocol      WsProtocol
	upstreamId      string
	endpoint        string
	requestRegistry RequestRegistry

	readEventsChan  chan wsEvent
	writeEventsChan chan wsEvent

	executor   failsafe.Executor[bool]
	subManager *utils.SubscriptionManager[protocol.SubscribeConnectorState]
}

func (b *BaseWsProcessor) Start() {
	b.lifecycle.Start(func(ctx context.Context) error {
		go func() {
			for {
				if ctx.Err() != nil {
					b.disconnect("stopping ws loop", ctx.Err())
					return
				}

				if b.wsSession.IsClosed() {
					err := b.connectWithRetry(ctx)
					if err != nil {
						b.disconnect("stopping ws loop after connect failure", err)
						return
					}
					b.subManager.Publish(protocol.WsConnected)
					b.startReader(ctx)
				}

				select {
				case <-ctx.Done():
					b.disconnect("stopping ws loop", ctx.Err())
					return
				case event := <-b.writeEventsChan:
					switch e := event.(type) {
					case *wsWriteEvent:
						select {
						case <-e.ctx.Done():
							e.resultErrChan <- e.ctx.Err()
						default:
							e.resultErrChan <- b.writeMessage(e.body)
						}
					}
				case event := <-b.readEventsChan:
					switch e := event.(type) {
					case *wsDisconnectEvent:
						b.disconnect(e.reason, e.cause)
						b.subManager.Publish(protocol.WsDisconnected)
					case *readEvent:
						switch e.response.Type {
						case protocol.JsonRpc:
							b.onRpcMessage(e.response)
						case protocol.Ws:
							b.onSubscriptionMessage(e.response)
						default:
							b.disconnect(fmt.Sprintf("unknown ws response format - %s", string(e.response.Message)), nil)
						}
					}
				}
			}
		}()

		return nil
	})
}

func (b *BaseWsProcessor) Stop() {
	b.lifecycle.Stop()
}

func (b *BaseWsProcessor) Running() bool {
	return b.lifecycle.Running()
}

func (b *BaseWsProcessor) SendRpcRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (*protocol.WsResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	timeout := time.NewTimer(rpcTimeout)
	defer timeout.Stop()
	respChan, err := b.sendWsRequest(ctx, upstreamRequest)
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
		return nil, fmt.Errorf("no response within %v on method %s via ws", rpcTimeout, upstreamRequest.Method())
	}
}

func (b *BaseWsProcessor) SendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error) {
	specMethod := upstreamRequest.SpecMethod()
	if specMethod == nil {
		return nil, fmt.Errorf("no spec method found for %s", upstreamRequest.Method())
	}
	if !specMethod.IsSubscribe() {
		return nil, fmt.Errorf("'%s' is not subscribe method and it can't be sent via SendWsRequest, use SendRpcRequest instead", upstreamRequest.Method())
	}

	return b.sendWsRequest(ctx, upstreamRequest)
}

func (b *BaseWsProcessor) SubscribeWsStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return b.subManager.Subscribe(name)
}

func NewBaseWsProcessor(
	ctx context.Context,
	upstreamId, endpoint string,
	dialWsService DialWsService,
	requestRegistry RequestRegistry,
	wsSession WsSession,
	wsProtocol WsProtocol,
) (*BaseWsProcessor, error) {
	wsConnDialFunc, err := dialWsService.NewConnectFunc(ctx)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s_ws_processor", upstreamId)

	return &BaseWsProcessor{
		lifecycle:       utils.NewBaseLifecycle(name, ctx),
		upstreamId:      upstreamId,
		wsProtocol:      wsProtocol,
		endpoint:        endpoint,
		subManager:      utils.NewSubscriptionManager[protocol.SubscribeConnectorState](name),
		wsConnDialFunc:  wsConnDialFunc,
		wsSession:       wsSession,
		requestRegistry: requestRegistry,
		readEventsChan:  make(chan wsEvent, 100),
		writeEventsChan: make(chan wsEvent, 100),
		executor:        failsafe.NewExecutor[bool](createConnectionRetryPolicy(endpoint)),
	}, nil
}

func (b *BaseWsProcessor) sendWsRequest(ctx context.Context, upstreamRequest protocol.RequestHolder) (chan *protocol.WsResponse, error) {
	frame, err := b.wsProtocol.RequestFrame(upstreamRequest)
	if err != nil {
		return nil, err
	}

	req := b.requestRegistry.Register(ctx, upstreamRequest, frame.RequestId, frame.SubType)
	b.requestRegistry.Start(req, b.wsProtocol.DoOnCloseFunc(b.writeRequest))

	err = b.writeRequest(ctx, frame.Body)
	if err != nil {
		b.requestRegistry.Abort(frame.RequestId, req)
		return nil, err
	}

	return req.GetResponseChannel(), nil
}

func (b *BaseWsProcessor) writeMessage(message []byte) error {
	return b.wsSession.WriteMessage(b.endpoint, message)
}

func (b *BaseWsProcessor) onRpcMessage(response *protocol.WsResponse) {
	b.requestRegistry.OnRpcMessage(response)
}

func (b *BaseWsProcessor) onSubscriptionMessage(response *protocol.WsResponse) {
	b.requestRegistry.OnSubscriptionMessage(response)
}

func (b *BaseWsProcessor) disconnect(reason string, cause error) {
	if cause != nil {
		log.Error().Err(cause).Msgf("%s from %s", reason, b.endpoint)
	} else {
		log.Warn().Msgf("%s for %s", reason, b.endpoint)
	}

	if err := b.wsSession.CloseCurrent(); err != nil {
		log.Error().Err(err).Msg("couldn't close a ws connection")
	}
	b.requestRegistry.CancelAll()
}

func (b *BaseWsProcessor) connectWithRetry(ctx context.Context) error {
	_, err := b.executor.GetWithExecution(func(exec failsafe.Execution[bool]) (bool, error) {
		if ctx.Err() != nil {
			return true, nil
		}

		err := b.openSession()
		if err != nil {
			return false, err
		}
		return true, nil
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}

	return err
}

func (b *BaseWsProcessor) openSession() error {
	conn, err := b.wsConnDialFunc()
	if err != nil {
		log.Error().Err(err).Msgf("couldn't connect to %s", b.endpoint)
		return err
	}

	log.Info().Msgf("connected to %s, listening to messages", b.endpoint)
	b.wsSession.SetConnection(conn)
	return nil
}

func (b *BaseWsProcessor) writeRequest(ctx context.Context, body []byte) error {
	writeEvent := newWsWriteEvent(ctx, body)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.writeEventsChan <- writeEvent:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-writeEvent.resultErrChan:
		return err
	}
}

func (b *BaseWsProcessor) startReader(ctx context.Context) {
	if b.wsSession.IsClosed() {
		return
	}
	sendEventFunc := func(event wsEvent) {
		select {
		case <-ctx.Done():
		case b.readEventsChan <- event:
		}
	}

	connection := b.wsSession.LoadConnection()

	go func() {
		for {
			_, message, err := connection.ReadMessage()
			if err != nil {
				sendEventFunc(newWsDisconnectEvent("couldn't read message", err))
				return
			}
			wsResponse, err := b.wsProtocol.ParseWsMessage(message)
			if err != nil {
				sendEventFunc(newWsDisconnectEvent("couldn't parse ws message", err))
				return
			}

			sendEventFunc(newReadEvent(wsResponse))
		}
	}()
}

var _ WsProcessor = (*BaseWsProcessor)(nil)
