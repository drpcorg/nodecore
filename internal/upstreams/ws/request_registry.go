package ws

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
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
	prometheus.MustRegister(jsonRpcWsConnectionsMetric, jsonRpcWsOperations)
}

type RequestRegistry interface {
	Start(req RequestOperation)
	Abort(requestId string)
	Register(ctx context.Context, request protocol.RequestHolder, requestId, subType string, doOnCLose DoOnClose) RequestOperation
	Cancel(opId string)
	CancelAll()

	OnRpcMessage(response *protocol.WsResponse)
	OnSubscriptionMessage(response *protocol.WsResponse)
}

type registrySubscription struct {
	subType string
	ops     map[string]RequestOperation
}

type registryState struct {
	requests map[string]RequestOperation
	subs     map[string]*registrySubscription
}

type BaseRequestRegistry struct {
	ctx           context.Context
	chain         chains.Chain
	upId          string
	methodSpec    string
	commands      chan registryCommand
	registryState *registryState

	allOps *utils.CMap[string, RequestOperation]
}

func (b *BaseRequestRegistry) Cancel(opId string) {
	op, ok := b.allOps.LoadAndDelete(opId)
	if !ok {
		return
	}
	b.closeReq(op)
}

func (b *BaseRequestRegistry) Register(
	ctx context.Context,
	request protocol.RequestHolder,
	requestId, subType string,
	doOnCLose DoOnClose,
) RequestOperation {
	req := NewBaseRequestOp(ctx, requestId, request.Method(), subType, doOnCLose)
	select {
	case <-b.ctx.Done():
		req.Cancel()
	case b.commands <- newRegisterCommand(requestId, req):
		b.allOps.Store(requestId, req)
	}
	return req
}

func (b *BaseRequestRegistry) Start(req RequestOperation) {
	go func() {
		jsonRpcWsOperations.WithLabelValues(b.chain.String(), b.upId).Inc()
		defer jsonRpcWsOperations.WithLabelValues(b.chain.String(), b.upId).Dec()

		for {
			select {
			case <-req.CtxDone():
				b.closeReq(req)
				return
			case message, ok := <-req.GetChannel(MessageInternal):
				if ok {
					req.Write(message, MessageResponse)
					if message.Error != nil {
						req.Cancel()
					}
				}
			}
		}
	}()
}

func (b *BaseRequestRegistry) Abort(requestId string) {
	b.sendCmd(newAbortCommand(requestId))
	b.allOps.Delete(requestId)
}

func (b *BaseRequestRegistry) OnRpcMessage(response *protocol.WsResponse) {
	b.sendCmd(newRpcCommand(response))
}

func (b *BaseRequestRegistry) OnSubscriptionMessage(response *protocol.WsResponse) {
	b.sendCmd(newSubscriptionCommand(response))
}

func (b *BaseRequestRegistry) CancelAll() {
	b.sendCmd(newCancelAllCommand())
}

func (b *BaseRequestRegistry) run() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case cmd := <-b.commands:
			cmd.handle(b)
		}
	}
}

func NewBaseRequestRegistry(ctx context.Context, chain chains.Chain, upId, methodSpec string) *BaseRequestRegistry {
	registry := &BaseRequestRegistry{
		ctx:        ctx,
		chain:      chain,
		upId:       upId,
		methodSpec: methodSpec,
		commands:   make(chan registryCommand, 1000),
		registryState: &registryState{
			requests: make(map[string]RequestOperation),
			subs:     make(map[string]*registrySubscription),
		},
		allOps: utils.NewCMap[string, RequestOperation](),
	}

	go registry.run()

	return registry
}

func (b *BaseRequestRegistry) sendCmd(cmd registryCommand) {
	select {
	case <-b.ctx.Done():
		log.Warn().Msgf("RequestRegistry of '%s' is not working, command is ignored", b.upId)
	case b.commands <- cmd:
	}
}

func (b *BaseRequestRegistry) closeReq(req RequestOperation) {
	done := make(chan bool, 1)
	b.sendCmd(newFinishCommand(req, done))

	select {
	case <-b.ctx.Done():
	case doClose := <-done:
		if doClose {
			req.DoOnClose()
		}
	case <-time.After(1 * time.Minute):
		log.Warn().Msgf("close WS RequestOperation %s timeout", req.Method())
	}
	req.Cancel()
	b.allOps.Delete(req.Id())
}

var _ RequestRegistry = (*BaseRequestRegistry)(nil)
