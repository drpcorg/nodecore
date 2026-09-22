package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/rs/zerolog/log"
)

// ChannelWsProtocol speaks the go-jsonrpc channel dialect of celestia-node.
// The subscribe call is a plain request whose result is a per-connection
// channel id; events are xrpc.ch.val notifications with params
// [channelId, value]; the node closes a channel with xrpc.ch.close
// [channelId]; a subscription is cancelled with xrpc.cancel [<id of the
// subscribe request>], which the node never answers.
type ChannelWsProtocol struct {
	upstreamId string
	methodSpec string
	internalId atomic.Int64
}

func NewChannelWsProtocol(upstreamId, methodSpec string) *ChannelWsProtocol {
	wsProtocol := &ChannelWsProtocol{
		upstreamId: upstreamId,
		methodSpec: methodSpec,
	}
	wsProtocol.internalId.Store(initialInternalId)

	return wsProtocol
}

func (c *ChannelWsProtocol) RequestFrame(request protocol.RequestHolder) (*RequestFrame, error) {
	return requestFrame(&c.internalId, request)
}

// DoOnCloseFunc cancels the node subscription with xrpc.cancel. Its param is
// the id nodecore stamped on the subscribe request, which is the op id - not
// the channel id the node answered with. The frame is a notification: an id on
// a cancel only makes the node log a warning.
func (c *ChannelWsProtocol) DoOnCloseFunc(writeRequestFunc WriteRequest) DoOnClose {
	return func(op RequestOperation) {
		if op.SubID() == "" {
			return
		}
		cancelMethod, ok := specs.GetUnsubscribeMethod(c.methodSpec, op.Method())
		if !ok {
			return
		}
		requestId, err := strconv.ParseInt(op.Id(), 10, 64)
		if err != nil {
			log.Error().Err(err).Msgf("couldn't cancel channel %s of upstream %s: op id %s is not numeric", op.SubID(), c.upstreamId, op.Id())
			return
		}
		body, err := sonic.Marshal(channelCancel{JsonRpc: "2.0", Method: cancelMethod, Params: []int64{requestId}})
		if err != nil {
			log.Error().Err(err).Msgf("couldn't build %s for request %d of upstream %s", cancelMethod, requestId, c.upstreamId)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = writeRequestFunc(ctx, body)
		cancel()
		if err != nil {
			log.Error().Err(err).Msgf("couldn't cancel channel %s of upstream %s with %s [%d]", op.SubID(), c.upstreamId, cancelMethod, requestId)
		} else {
			log.Info().Msgf("channel %s of upstream %s has been successfully cancelled", op.SubID(), c.upstreamId)
		}
	}
}

// ParseWsMessage reads the two channel notifications by method name and
// leaves everything else (call results, the subscribe ack, errors) to the
// shared JSON-RPC parser. A malformed channel frame is an error, which the
// processor turns into a disconnect like any unparsable frame.
func (c *ChannelWsProtocol) ParseWsMessage(payload []byte) (*protocol.WsResponse, error) {
	frame := channelFrame{}
	if err := sonic.Unmarshal(payload, &frame); err == nil {
		switch frame.Method {
		case "xrpc.ch.val":
			if len(frame.Params) != 2 {
				return nil, fmt.Errorf("xrpc.ch.val expects [channelId, value], got %d params", len(frame.Params))
			}
			channelId, err := channelIdOf(frame.Params[0])
			if err != nil {
				return nil, err
			}
			return &protocol.WsResponse{Type: protocol.Ws, SubId: channelId, Message: frame.Params[1]}, nil
		case "xrpc.ch.close":
			if len(frame.Params) != 1 {
				return nil, fmt.Errorf("xrpc.ch.close expects [channelId], got %d params", len(frame.Params))
			}
			channelId, err := channelIdOf(frame.Params[0])
			if err != nil {
				return nil, err
			}
			// the node ended the subscription; the registry drops it and the
			// engine treats the error as the terminal frame
			return &protocol.WsResponse{Type: protocol.Ws, SubId: channelId, Error: protocol.SubscribeTotalFailureError()}, nil
		}
	}

	wsResponse := protocol.ParseJsonRpcWsMessage(payload)
	if wsResponse.Type != protocol.JsonRpc {
		return nil, fmt.Errorf("invalid response type - %s", wsResponse.Type)
	}
	return wsResponse, nil
}

type channelFrame struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type channelCancel struct {
	JsonRpc string  `json:"jsonrpc"`
	Method  string  `json:"method"`
	Params  []int64 `json:"params"`
}

// channelIdOf returns the channel id as the registry keys it: the decimal text
// of the JSON number, the same form ResultAsString gives the subscribe ack.
func channelIdOf(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return "", fmt.Errorf("channel id must be a number, got %s", string(raw))
	}
	return string(raw), nil
}

var _ WsProtocol = (*ChannelWsProtocol)(nil)
