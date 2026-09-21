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
	retryPolicy := retrypolicy.NewBuilder[bool]()

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
	// generation is the session generation of the connection whose reader
	// produced this event. The main loop ignores the event if it no longer
	// matches the session's current generation (a superseded connection).
	generation uint64
}

func newWsDisconnectEvent(reason string, cause error, generation uint64) *wsDisconnectEvent {
	return &wsDisconnectEvent{reason: reason, cause: cause, generation: generation}
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

const (
	defaultRequestOpBufferSize      = 50
	subscriptionRequestOpBufferSize = 4096
)

type RequestOperation interface {
	Write(message protocol.SubResponse, messageType MessageType)
	SetSubID(subID []byte)
	SetSkipDoOnClose()

	Id() string
	IsCompleted() bool
	SubID() string
	SubIdBytes() []byte
	ShouldDoOnClose() bool
	Method() string
	GetChannel(messageType MessageType) chan protocol.SubResponse
	SubType() string

	CtxDone() <-chan struct{}

	Cancel()
	DoOnClose()
}

type GenericRequestOp struct {
	mu sync.RWMutex

	responseChan     chan protocol.SubResponse
	internalMessages chan protocol.SubResponse

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

func (r *GenericRequestOp) DoOnClose() {
	r.doOnClose(r)
}

func (r *GenericRequestOp) Write(message protocol.SubResponse, messageType MessageType) {
	switch messageType {
	case MessageInternal:
		select {
		case <-r.CtxDone():
			return
		case r.internalMessages <- message:
		default:
			log.Warn().Msgf("internal channel full, dropping message %s", r.method)
		}
	case MessageResponse:
		select {
		case <-r.CtxDone():
			return
		case r.responseChan <- message:
		default:
			log.Warn().Msgf("response channel full, dropping message %s", r.method)
		}
	}
}

func (r *GenericRequestOp) GetChannel(messageType MessageType) chan protocol.SubResponse {
	switch messageType {
	case MessageInternal:
		return r.internalMessages
	case MessageResponse:
		return r.responseChan
	}
	return nil
}

func (r *GenericRequestOp) SubIdBytes() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subIdBytes := make([]byte, len(r.subIdAsBytes))
	copy(subIdBytes, r.subIdAsBytes)
	return subIdBytes
}

func (r *GenericRequestOp) Id() string {
	return r.id
}

func (r *GenericRequestOp) CtxDone() <-chan struct{} {
	return r.ctx.Done()
}

func (r *GenericRequestOp) SubType() string {
	return r.subType
}

func (r *GenericRequestOp) Cancel() {
	if r.completed.CompareAndSwap(false, true) {
		r.cancel()

		go func() {
			time.Sleep(100 * time.Millisecond)
			close(r.responseChan)
		}()
	}
}

func (r *GenericRequestOp) Method() string {
	return r.method
}

func (r *GenericRequestOp) IsCompleted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.completed.Load()
}

func (r *GenericRequestOp) SetSubID(subID []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subId = protocol.ResultAsString(subID)
	r.subIdAsBytes = subID
}

func (r *GenericRequestOp) SubID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.subId
}

func (r *GenericRequestOp) SetSkipDoOnClose() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipDoOnClose = true
}

func (r *GenericRequestOp) ShouldDoOnClose() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.skipDoOnClose
}

func NewGenericRequestOp(ctx context.Context, id, method, subType string, doOnClose DoOnClose) *GenericRequestOp {
	ctx, cancel := context.WithCancel(ctx)
	bufferSize := requestOpBufferSize(subType)

	return &GenericRequestOp{
		id:               id,
		responseChan:     make(chan protocol.SubResponse, bufferSize),
		internalMessages: make(chan protocol.SubResponse, bufferSize),
		ctx:              ctx,
		cancel:           cancel,
		method:           method,
		subType:          subType,
		doOnClose:        doOnClose,
	}
}

func requestOpBufferSize(subType string) int {
	if subType != "" {
		return subscriptionRequestOpBufferSize
	}
	return defaultRequestOpBufferSize
}

var _ RequestOperation = (*GenericRequestOp)(nil)
