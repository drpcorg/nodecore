package ws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stopped processor whose loop is inside a reconnect backoff must not sleep the backoff
// out: the retry executor runs under the loop's context, so the cancel ends it at once.
func TestConnectWithRetryReturnsAsSoonAsTheContextEnds(t *testing.T) {
	processor := &GenericWsProcessor{
		endpoint: "ws://endpoint",
		wsConnDialFunc: func() (*websocket.Conn, error) {
			return nil, errors.New("dial failed")
		},
		executor: failsafe.With[bool](createConnectionRetryPolicy("ws://endpoint")),
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	started := time.Now()
	err := processor.connectWithRetry(ctx)

	require.ErrorIs(t, err, context.Canceled)
	// the first backoff alone is at least a second
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}
