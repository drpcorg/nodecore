package mocks

import (
	"sync"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
)

type RequestOperationMock struct {
	mu                   sync.RWMutex
	MethodValue          string
	SubIDValue           string
	SubIDBytesValue      []byte
	SubTypeValue         string
	CompletedValue       bool
	ShouldDoOnCloseValue bool
	ResponseChannel      chan *protocol.WsResponse
	InternalChannel      chan *protocol.WsResponse
	DoneChannel          <-chan struct{}
	IDValue              string
	DoOnCloseFunc        ws.DoOnClose
}

func NewRequestOperationMock() *RequestOperationMock {
	done := make(chan struct{})

	return &RequestOperationMock{
		ShouldDoOnCloseValue: true,
		ResponseChannel:      make(chan *protocol.WsResponse, 1),
		InternalChannel:      make(chan *protocol.WsResponse, 1),
		DoneChannel:          done,
		IDValue:              "op-1",
	}
}

func (r *RequestOperationMock) Write(message *protocol.WsResponse, messageType ws.MessageType) {
	switch messageType {
	case ws.MessageInternal:
		r.InternalChannel <- message
	case ws.MessageResponse:
		r.ResponseChannel <- message
	}
}

func (r *RequestOperationMock) SetSubID(subID []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.SubIDValue = protocol.ResultAsString(subID)
	r.SubIDBytesValue = append([]byte(nil), subID...)
}

func (r *RequestOperationMock) SetSkipDoOnClose() {
	r.ShouldDoOnCloseValue = false
}

func (r *RequestOperationMock) Id() string {
	return r.IDValue
}

func (r *RequestOperationMock) IsCompleted() bool {
	return r.CompletedValue
}

func (r *RequestOperationMock) SubID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.SubIDValue
}

func (r *RequestOperationMock) SubIdBytes() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.SubIDBytesValue) == 0 && r.SubIDValue != "" {
		return []byte(`"` + r.SubIDValue + `"`)
	}
	return append([]byte(nil), r.SubIDBytesValue...)
}

func (r *RequestOperationMock) ShouldDoOnClose() bool {
	return r.ShouldDoOnCloseValue
}

func (r *RequestOperationMock) Method() string {
	return r.MethodValue
}

func (r *RequestOperationMock) GetChannel(messageType ws.MessageType) chan *protocol.WsResponse {
	switch messageType {
	case ws.MessageInternal:
		return r.InternalChannel
	case ws.MessageResponse:
		return r.ResponseChannel
	default:
		return nil
	}
}

func (r *RequestOperationMock) SubType() string {
	return r.SubTypeValue
}

func (r *RequestOperationMock) CtxDone() <-chan struct{} {
	return r.DoneChannel
}

func (r *RequestOperationMock) Cancel() {}

func (r *RequestOperationMock) DoOnClose() {
	if r.DoOnCloseFunc != nil {
		r.DoOnCloseFunc(r)
	}
}

func (r *RequestOperationMock) Complete() {
	r.CompletedValue = true
}

var _ ws.RequestOperation = (*RequestOperationMock)(nil)
