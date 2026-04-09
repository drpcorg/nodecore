package ws

import (
	"context"
	"sync"
	"sync/atomic"
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

type MessageType int

const (
	MessageInternal MessageType = iota
	MessageResponse
)

type RequestOperation interface {
	Write(message *protocol.WsResponse, messageType MessageType)
	SetSubID(subID []byte)
	SetSkipDoOnClose()

	Id() string
	IsCompleted() bool
	SubID() string
	SubIdBytes() []byte
	ShouldDoOnClose() bool
	Method() string
	GetChannel(messageType MessageType) chan *protocol.WsResponse
	SubType() string

	CtxDone() <-chan struct{}

	Cancel()
	DoOnClose()
}

type BaseRequestOp struct {
	mu sync.RWMutex

	responseChan     chan *protocol.WsResponse
	internalMessages chan *protocol.WsResponse

	ctx           context.Context
	cancel        context.CancelFunc
	method        string
	subId         string
	subIdAsBytes  []byte
	subType       string
	completed     atomic.Bool
	skipDoOnClose bool
	id            string
	doOnClose     DoOnClose
}

func (r *BaseRequestOp) DoOnClose() {
	r.doOnClose(r)
}

func (r *BaseRequestOp) Write(message *protocol.WsResponse, messageType MessageType) {
	switch messageType {
	case MessageInternal:
		select {
		case <-r.CtxDone():
			return
		case r.internalMessages <- message:
		}
	case MessageResponse:
		select {
		case <-r.CtxDone():
			return
		case r.responseChan <- message:
		}
	}
}

func (r *BaseRequestOp) GetChannel(messageType MessageType) chan *protocol.WsResponse {
	switch messageType {
	case MessageInternal:
		return r.internalMessages
	case MessageResponse:
		return r.responseChan
	}
	return nil
}

func (r *BaseRequestOp) SubIdBytes() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subIdBytes := make([]byte, len(r.subIdAsBytes))
	copy(subIdBytes, r.subIdAsBytes)
	return subIdBytes
}

func (r *BaseRequestOp) Id() string {
	return r.id
}

func (r *BaseRequestOp) CtxDone() <-chan struct{} {
	return r.ctx.Done()
}

func (r *BaseRequestOp) SubType() string {
	return r.subType
}

func (r *BaseRequestOp) Cancel() {
	if r.completed.CompareAndSwap(false, true) {
		r.cancel()

		go func() {
			time.Sleep(100 * time.Millisecond)
			close(r.responseChan)
		}()
	}
}

func (r *BaseRequestOp) Method() string {
	return r.method
}

func (r *BaseRequestOp) IsCompleted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.completed.Load()
}

func (r *BaseRequestOp) SetSubID(subID []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subId = protocol.ResultAsString(subID)
	r.subIdAsBytes = subID
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

func NewBaseRequestOp(ctx context.Context, id, method, subType string, doOnClose DoOnClose) *BaseRequestOp {
	ctx, cancel := context.WithCancel(ctx)

	return &BaseRequestOp{
		id:               id,
		responseChan:     make(chan *protocol.WsResponse, 50),
		internalMessages: make(chan *protocol.WsResponse, 50),
		ctx:              ctx,
		cancel:           cancel,
		method:           method,
		subType:          subType,
		doOnClose:        doOnClose,
	}
}

var _ RequestOperation = (*BaseRequestOp)(nil)
