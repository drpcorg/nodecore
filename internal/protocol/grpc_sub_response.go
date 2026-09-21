package protocol

import "net/http"

// GrpcSubResponse is one frame of an upstream gRPC server stream: a message
// (Message), the terminal status (Error, carrying the verbatim GrpcStatus), or
// the clean end of the stream (End). Headers ride on the first frame, Trailers
// on the terminal one (error or end frame).
type GrpcSubResponse struct {
	Message    []byte
	Error      *ResponseError
	End        bool
	UpstreamId string
	Headers    http.Header
	Trailers   map[string][]string
}

func (g *GrpcSubResponse) GetMessage() []byte {
	return g.Message
}

func (g *GrpcSubResponse) GetError() *ResponseError {
	return g.Error
}

func (g *GrpcSubResponse) GetUpstreamId() string {
	return g.UpstreamId
}

// GetParsedEvent is nil: gRPC frames are opaque bytes, never parsed.
func (g *GrpcSubResponse) GetParsedEvent() ParsedEvent {
	return nil
}

func (g *GrpcSubResponse) IsEnd() bool {
	return g.End
}

func (g *GrpcSubResponse) ResponseHeaders() http.Header {
	return g.Headers
}

func (g *GrpcSubResponse) ResponseTrailers() map[string][]string {
	return g.Trailers
}

// GrpcUpstreamSubscriptionResponse is the handle GrpcConnector.Subscribe returns:
// the frame channel plus an op id (informational - the stream's lifetime is the
// ctx passed to Subscribe, so Unsubscribe is a no-op for gRPC).
type GrpcUpstreamSubscriptionResponse struct {
	messages chan SubResponse
	opId     string
}

func NewGrpcUpstreamSubscriptionResponse(messages chan SubResponse, opId string) *GrpcUpstreamSubscriptionResponse {
	return &GrpcUpstreamSubscriptionResponse{messages: messages, opId: opId}
}

func (g *GrpcUpstreamSubscriptionResponse) ResponseChan() chan SubResponse {
	return g.messages
}

func (g *GrpcUpstreamSubscriptionResponse) OpId() string {
	return g.opId
}

var _ SubResponse = (*GrpcSubResponse)(nil)
var _ HasResponseHeaders = (*GrpcSubResponse)(nil)
var _ HasResponseTrailers = (*GrpcSubResponse)(nil)
var _ UpstreamSubscriptionResponse = (*GrpcUpstreamSubscriptionResponse)(nil)
