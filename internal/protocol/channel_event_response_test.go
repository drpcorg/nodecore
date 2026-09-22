package protocol_test

import (
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSubscriptionEventResponseEncodesValueNotification(t *testing.T) {
	payload := []byte(`{"header":{"height":"42"}}`)
	response := protocol.NewChannelSubscriptionEventResponse("req", "xrpc.ch.val", payload, 7)

	encoded, err := io.ReadAll(response.EncodeResponse([]byte(`1`)))
	require.NoError(t, err)

	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.val","params":[7,{"header":{"height":"42"}}]}`, string(encoded))
	assert.Equal(t, payload, response.ResponseResult())
	assert.False(t, response.HasError())
	assert.False(t, response.IsEnd())
	assert.Equal(t, "req", response.Id())
}

// The same shape without a payload is the channel-close notification.
func TestChannelSubscriptionEventResponseEncodesCloseNotification(t *testing.T) {
	response := protocol.NewChannelSubscriptionEventResponse("req", "xrpc.ch.close", nil, 7)

	encoded, err := io.ReadAll(response.EncodeResponse([]byte(`1`)))
	require.NoError(t, err)

	assert.JSONEq(t, `{"jsonrpc":"2.0","method":"xrpc.ch.close","params":[7]}`, string(encoded))
}
