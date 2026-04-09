package ws

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/nodecore/pkg/methods"
)

type registryCommand interface {
	handle(registry *BaseRequestRegistry)
}

type registerCommand struct {
	requestID string
	req       RequestOperation
}

func newRegisterCommand(requestID string, req RequestOperation) *registerCommand {
	return &registerCommand{
		requestID: requestID,
		req:       req,
	}
}

func (c *registerCommand) handle(registry *BaseRequestRegistry) {
	registry.registryState.requests[c.requestID] = c.req
}

type abortCommand struct {
	requestID string
}

func newAbortCommand(requestID string) *abortCommand {
	return &abortCommand{
		requestID: requestID,
	}
}

func (c *abortCommand) handle(registry *BaseRequestRegistry) {
	if req, ok := registry.registryState.requests[c.requestID]; ok {
		delete(registry.registryState.requests, c.requestID)
		req.SetSkipDoOnClose()
		req.Cancel()
	}
}

type rpcCommand struct {
	response *protocol.WsResponse
}

func newRpcCommand(response *protocol.WsResponse) *rpcCommand {
	return &rpcCommand{
		response: response,
	}
}

func (c *rpcCommand) handle(registry *BaseRequestRegistry) {
	state := registry.registryState
	req, ok := state.requests[c.response.Id]
	if !ok {
		return
	}
	delete(state.requests, c.response.Id)

	if req.IsCompleted() {
		return
	}

	if c.response.Error == nil && specs.IsSubscribeMethod(registry.methodSpec, req.Method()) {
		req.SetSubID(c.response.Message)
		subID := req.SubID()
		if subID != "" && !req.IsCompleted() {
			sub, exists := state.subs[subID]
			if !exists {
				sub = &registrySubscription{
					subType: req.SubType(),
					ops:     make(map[string]RequestOperation),
				}
				state.subs[subID] = sub
				jsonRpcWsConnectionsMetric.WithLabelValues(registry.chain.String(), registry.upId, req.SubType()).Inc()
			}
			sub.ops[req.Id()] = req
		}
	}

	req.Write(c.response, MessageInternal)
}

type subscriptionCommand struct {
	response *protocol.WsResponse
}

func newSubscriptionCommand(response *protocol.WsResponse) *subscriptionCommand {
	return &subscriptionCommand{
		response: response,
	}
}

func (c *subscriptionCommand) handle(registry *BaseRequestRegistry) {
	sub, ok := registry.registryState.subs[c.response.SubId]
	if !ok {
		return
	}

	for _, req := range sub.ops {
		req.Write(c.response, MessageInternal)
	}
}

type finishCommand struct {
	req    RequestOperation
	result chan bool
}

func newFinishCommand(req RequestOperation, result chan bool) *finishCommand {
	return &finishCommand{
		req:    req,
		result: result,
	}
}

func (c *finishCommand) handle(registry *BaseRequestRegistry) {
	c.req.Cancel()

	subID := c.req.SubID()
	if subID == "" {
		c.result <- false
		return
	}

	sub, ok := registry.registryState.subs[subID]
	if !ok {
		c.result <- false
		return
	}

	delete(sub.ops, c.req.Id())
	if len(sub.ops) != 0 {
		c.result <- false
		return
	}

	delete(registry.registryState.subs, subID)
	jsonRpcWsConnectionsMetric.WithLabelValues(registry.chain.String(), registry.upId, sub.subType).Dec()
	c.result <- c.req.ShouldDoOnClose()
}

type cancelAllCommand struct {
}

func newCancelAllCommand() *cancelAllCommand {
	return &cancelAllCommand{}
}

func (c *cancelAllCommand) handle(registry *BaseRequestRegistry) {
	state := registry.registryState
	ops := make([]RequestOperation, 0, len(state.requests)+len(state.subs))

	for requestID, req := range state.requests {
		ops = append(ops, req)
		delete(state.requests, requestID)
	}

	for subID, sub := range state.subs {
		jsonRpcWsConnectionsMetric.WithLabelValues(registry.chain.String(), registry.upId, sub.subType).Dec()
		for _, req := range sub.ops {
			ops = append(ops, req)
		}
		delete(state.subs, subID)
	}
	for _, op := range ops {
		op.SetSkipDoOnClose()
		op.Cancel()
	}
}
