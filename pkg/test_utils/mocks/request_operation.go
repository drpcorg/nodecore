package mocks

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/ws"
)

type RequestOperationMock struct {
	MethodValue          string
	SubIDValue           string
	SubTypeValue         string
	CompletedValue       bool
	ShouldDoOnCloseValue bool
	ResponseChannel      chan *protocol.WsResponse
	InternalChannel      chan *protocol.WsResponse
	DoneChannel          <-chan struct{}
}

func NewRequestOperationMock() *RequestOperationMock {
	done := make(chan struct{})

	return &RequestOperationMock{
		ShouldDoOnCloseValue: true,
		ResponseChannel:      make(chan *protocol.WsResponse, 1),
		InternalChannel:      make(chan *protocol.WsResponse, 1),
		DoneChannel:          done,
	}
}

func (r *RequestOperationMock) WriteInternal(message *protocol.WsResponse) {
	r.InternalChannel <- message
}

func (r *RequestOperationMock) WriteResponse(message *protocol.WsResponse) {
	r.ResponseChannel <- message
}

func (r *RequestOperationMock) SetSubID(subID string) {
	r.SubIDValue = subID
}

func (r *RequestOperationMock) SetSkipDoOnClose() {
	r.ShouldDoOnCloseValue = false
}

func (r *RequestOperationMock) IsCompleted() bool {
	return r.CompletedValue
}

func (r *RequestOperationMock) SubID() string {
	return r.SubIDValue
}

func (r *RequestOperationMock) ShouldDoOnClose() bool {
	return r.ShouldDoOnCloseValue
}

func (r *RequestOperationMock) Method() string {
	return r.MethodValue
}

func (r *RequestOperationMock) GetInternalChannel() chan *protocol.WsResponse {
	return r.InternalChannel
}

func (r *RequestOperationMock) GetResponseChannel() chan *protocol.WsResponse {
	return r.ResponseChannel
}

func (r *RequestOperationMock) SubType() string {
	return r.SubTypeValue
}

func (r *RequestOperationMock) Done() <-chan struct{} {
	return r.DoneChannel
}

func (r *RequestOperationMock) Cancel() {}

func (r *RequestOperationMock) Complete() {
	r.CompletedValue = true
}

var _ ws.RequestOperation = (*RequestOperationMock)(nil)
