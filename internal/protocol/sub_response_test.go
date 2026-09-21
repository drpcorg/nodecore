package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
)

type stubParsedEvent struct{ raw []byte }

func (s stubParsedEvent) Raw() []byte { return s.raw }

func TestWsResponseImplementsSubResponse(t *testing.T) {
	pe := stubParsedEvent{raw: []byte(`{}`)}
	var r protocol.SubResponse = &protocol.WsResponse{
		Message:     []byte(`"0x1"`),
		Error:       protocol.ResponseErrorWithMessage("boom"),
		UpstreamId:  "up-1",
		ParsedEvent: pe,
	}

	assert.Equal(t, []byte(`"0x1"`), r.GetMessage())
	assert.Equal(t, "boom", r.GetError().Message)
	assert.Equal(t, "up-1", r.GetUpstreamId())
	assert.Equal(t, pe, r.GetParsedEvent())
}

func TestGenericSubResponseImplementsSubResponse(t *testing.T) {
	pe := stubParsedEvent{raw: []byte(`{}`)}
	var r protocol.SubResponse = &protocol.GenericSubResponse{
		Message:     []byte(`"0x2"`),
		Error:       protocol.ResponseErrorWithMessage("bang"),
		UpstreamId:  "up-2",
		ParsedEvent: pe,
	}

	assert.Equal(t, []byte(`"0x2"`), r.GetMessage())
	assert.Equal(t, "bang", r.GetError().Message)
	assert.Equal(t, "up-2", r.GetUpstreamId())
	assert.Equal(t, pe, r.GetParsedEvent())
}

func TestSubscribeErrorRenames(t *testing.T) {
	total := protocol.SubscribeTotalFailureError()
	assert.Equal(t, protocol.SubscribeTotalFailure, total.Code)
	assert.Equal(t, "subscription total failure", total.Message)

	slow := protocol.SubscriberTooSlowError()
	assert.Equal(t, protocol.SubscribeTotalFailure, slow.Code)
}
