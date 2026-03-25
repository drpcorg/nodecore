package ws

import (
	"context"
	"sync"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/rs/zerolog/log"
)

type DoOnClose func(RequestOperation)
type WriteRequest func(ctx context.Context, body []byte) error

type RequestFrame struct {
	RequestId string
	SubType   string
	Body      []byte
}

func NewRequestFrame(requestId string, subType string, body []byte) *RequestFrame {
	return &RequestFrame{
		RequestId: requestId,
		SubType:   subType,
		Body:      body,
	}
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
		log.Warn().Msgf("attempting to reconnect to %s", url)
	})

	return retryPolicy.Build()
}

type wsEvent interface {
	wsEvent()
}

type wsWriteEvent struct {
	ctx           context.Context
	body          []byte
	resultErrChan chan error
}

func newWsWriteEvent(ctx context.Context, body []byte) *wsWriteEvent {
	return &wsWriteEvent{
		ctx:           ctx,
		body:          body,
		resultErrChan: make(chan error, 1),
	}
}

func (w *wsWriteEvent) wsEvent() {}

type wsDisconnectEvent struct {
	reason string
	cause  error
}

func newWsDisconnectEvent(reason string, cause error) *wsDisconnectEvent {
	return &wsDisconnectEvent{reason: reason, cause: cause}
}

func (e *wsDisconnectEvent) wsEvent() {}

type readEvent struct {
	response *protocol.WsResponse
}

func newReadEvent(response *protocol.WsResponse) *readEvent {
	return &readEvent{response: response}
}

func (e *readEvent) wsEvent() {}

type RequestOperation interface {
	WriteInternal(message *protocol.WsResponse)
	WriteResponse(message *protocol.WsResponse)
	SetSubID(subID string)
	SetSkipDoOnClose()

	IsCompleted() bool
	SubID() string
	ShouldDoOnClose() bool
	Method() string
	GetInternalChannel() chan *protocol.WsResponse
	GetResponseChannel() chan *protocol.WsResponse
	SubType() string
	Done() <-chan struct{}

	Cancel()
}

type BaseRequestOp struct {
	mu sync.RWMutex

	responseChan     chan *protocol.WsResponse
	internalMessages chan *protocol.WsResponse

	ctx           context.Context
	cancel        context.CancelFunc
	method        string
	subId         string
	subType       string
	completed     bool
	skipDoOnClose bool
}

func (r *BaseRequestOp) GetResponseChannel() chan *protocol.WsResponse {
	return r.responseChan
}

func (r *BaseRequestOp) Done() <-chan struct{} {
	return r.ctx.Done()
}

func (r *BaseRequestOp) WriteResponse(message *protocol.WsResponse) {
	select {
	case <-r.Done():
		return
	case r.responseChan <- message:
	}
}

func (r *BaseRequestOp) SubType() string {
	return r.subType
}

func (r *BaseRequestOp) Cancel() {
	r.cancel()

	r.mu.Lock()
	if r.completed {
		r.mu.Unlock()
		return
	}
	r.completed = true
	r.mu.Unlock()

	go func() {
		time.Sleep(100 * time.Millisecond)
		close(r.responseChan)
	}()
}

func (r *BaseRequestOp) GetInternalChannel() chan *protocol.WsResponse {
	return r.internalMessages
}

func (r *BaseRequestOp) Method() string {
	return r.method
}

func (r *BaseRequestOp) WriteInternal(message *protocol.WsResponse) {
	select {
	case <-r.Done():
		return
	case r.internalMessages <- message:
	}
}

func (r *BaseRequestOp) IsCompleted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.completed
}

func (r *BaseRequestOp) SetSubID(subID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subId = subID
}

func (r *BaseRequestOp) SubID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.subId
}

func (r *BaseRequestOp) SetSkipDoOnClose() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipDoOnClose = true
}

func (r *BaseRequestOp) ShouldDoOnClose() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.skipDoOnClose
}

func NewBaseRequestOp(ctx context.Context, method, subType string) *BaseRequestOp {
	ctx, cancel := context.WithCancel(ctx)

	return &BaseRequestOp{
		responseChan:     make(chan *protocol.WsResponse, 50),
		internalMessages: make(chan *protocol.WsResponse, 50),
		ctx:              ctx,
		cancel:           cancel,
		method:           method,
		subType:          subType,
	}
}

var _ RequestOperation = (*BaseRequestOp)(nil)
