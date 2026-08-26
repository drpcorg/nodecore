package protocol

type WsResponse struct {
	Id         string
	SubId      string
	Message    []byte
	Type       RequestType
	Error      *ResponseError
	Event      []byte
	UpstreamId string
	// ParsedEvent is an optional, source-attached pre-parsed view of Message,
	ParsedEvent ParsedEvent
}

// ParsedEvent is the pre-parsed view of a WsResponse Message. See WsResponse.ParsedEvent.
type ParsedEvent interface {
	Raw() []byte
}

func (w *WsResponse) GetMessage() []byte {
	return w.Message
}

func (w *WsResponse) GetError() *ResponseError {
	return w.Error
}

func (w *WsResponse) GetUpstreamId() string {
	return w.UpstreamId
}

func (w *WsResponse) GetParsedEvent() ParsedEvent {
	return w.ParsedEvent
}

// IsEnd is false: a websocket subscription never ends cleanly on its own.
func (w *WsResponse) IsEnd() bool {
	return false
}

var _ SubResponse = (*WsResponse)(nil)

// GenericSubResponse is the transport-neutral subscription event synthesized
// by the generic pipeline itself (sources, subengine, heads). Transport
// layers push their own SubResponse implementations instead.
type GenericSubResponse struct {
	Message     []byte
	Error       *ResponseError
	UpstreamId  string
	ParsedEvent ParsedEvent
}

func (g *GenericSubResponse) GetMessage() []byte {
	return g.Message
}

func (g *GenericSubResponse) GetError() *ResponseError {
	return g.Error
}

func (g *GenericSubResponse) GetUpstreamId() string {
	return g.UpstreamId
}

func (g *GenericSubResponse) GetParsedEvent() ParsedEvent {
	return g.ParsedEvent
}

// IsEnd is false: synthesized sources end by closing their channel.
func (g *GenericSubResponse) IsEnd() bool {
	return false
}

var _ SubResponse = (*GenericSubResponse)(nil)
