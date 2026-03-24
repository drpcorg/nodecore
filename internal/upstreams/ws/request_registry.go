package ws

import (
	"context"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
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
	Start(req RequestOperation, doOnCloseFunc DoOnClose)
	Abort(requestId string, req RequestOperation)
	Register(ctx context.Context, request protocol.RequestHolder, requestId, subType string) RequestOperation
	CancelAll()

	OnRpcMessage(response *protocol.WsResponse)
	OnSubscriptionMessage(response *protocol.WsResponse)
}

type BaseRequestRegistry struct {
	chain      chains.Chain
	upId       string
	methodSpec string
	requests   *utils.CMap[string, RequestOperation]
	subs       *utils.CMap[string, RequestOperation]
}

func (b *BaseRequestRegistry) Register(ctx context.Context, request protocol.RequestHolder, requestId, subType string) RequestOperation {
	req := NewBaseRequestOp(ctx, request.Method(), subType)

	b.requests.Store(requestId, req)

	return req
}

func (b *BaseRequestRegistry) Start(req RequestOperation, doOnCloseFunc DoOnClose) {
	go func() {
		jsonRpcWsOperations.WithLabelValues(b.chain.String(), b.upId).Inc()
		defer jsonRpcWsOperations.WithLabelValues(b.chain.String(), b.upId).Dec()
		for {
			select {
			case <-req.Done():
				if req.ShouldDoOnClose() {
					doOnCloseFunc(req)
				}

				if subID := req.SubID(); subID != "" {
					b.subs.Delete(subID)
					jsonRpcWsConnectionsMetric.WithLabelValues(b.chain.String(), b.upId, req.SubType()).Dec()
				}
				return
			case message, ok := <-req.GetInternalChannel():
				if ok {
					req.WriteResponse(message)
					if message.Error != nil {
						req.Cancel()
					}
				}
			}
		}
	}()
}

func (b *BaseRequestRegistry) Abort(requestId string, req RequestOperation) {
	b.requests.Delete(requestId)
	req.SetSkipDoOnClose()
	req.Cancel()
}

func (b *BaseRequestRegistry) OnRpcMessage(response *protocol.WsResponse) {
	if req, ok := b.requests.Load(response.Id); ok {
		defer b.requests.Delete(response.Id)

		if req.IsCompleted() {
			return
		}

		req.WriteInternal(response)
		if response.Error == nil && specs.IsSubscribeMethod(b.methodSpec, req.Method()) {
			subID := protocol.ResultAsString(response.Message)
			req.SetSubID(subID)
			b.subs.Store(subID, req)
			if subID != "" {
				jsonRpcWsConnectionsMetric.WithLabelValues(b.chain.String(), b.upId, req.SubType()).Inc()
			}
		}
	}
}

func (b *BaseRequestRegistry) OnSubscriptionMessage(response *protocol.WsResponse) {
	if req, ok := b.subs.Load(response.SubId); ok {
		if req.IsCompleted() {
			b.subs.Delete(response.SubId)
			return
		}
		req.WriteInternal(response)
	}
}

func (b *BaseRequestRegistry) CancelAll() {
	b.requests.Range(func(key string, val RequestOperation) bool {
		b.requests.Delete(key)
		val.SetSkipDoOnClose()
		val.Cancel()
		return true
	})
	b.subs.Range(func(key string, val RequestOperation) bool {
		b.subs.Delete(key)
		val.SetSkipDoOnClose()
		val.Cancel()
		return true
	})
}

func NewBaseRequestRegistry(chain chains.Chain, upId, methodSpec string) *BaseRequestRegistry {
	return &BaseRequestRegistry{
		chain:      chain,
		upId:       upId,
		methodSpec: methodSpec,
		requests:   utils.NewCMap[string, RequestOperation](),
		subs:       utils.NewCMap[string, RequestOperation](),
	}
}

var _ RequestRegistry = (*BaseRequestRegistry)(nil)
