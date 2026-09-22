package flow

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/drpcorg/nodecore/internal/protocol"
)

// channelSubCtx is the go-jsonrpc channel dialect (celestia-node clients): the
// ack carries a per-connection channel id counted from 1, events are
// xrpc.ch.val [channelId, value], and the client cancels with xrpc.cancel
// [<its own subscribe request id>]. Subscriptions are filed under that request
// id; a client may reuse one id for several subscribe calls, so a key holds a
// list.
type channelSubCtx struct {
	mu         sync.Mutex
	subs       map[string][]channelSub
	channelIds atomic.Uint64
}

type channelSub struct {
	channelId uint64
	cancel    context.CancelFunc
}

func (c *channelSubCtx) Framing() subFraming {
	return &channelFraming{subCtx: c}
}

// Unsubscribe cancels every subscription filed under key, oldest first, and
// replies with one xrpc.ch.close [channelId] per closed channel, in that
// order; an unknown key gets nothing, as a go-jsonrpc node answers a cancel.
// The close is written by this reply, not by the subscription's own goroutine,
// so a value that arrived in the same instant as the cancel can reach the
// client after the close; a go-jsonrpc client drops a value for a channel it
// no longer knows.
func (c *channelSubCtx) Unsubscribe(request protocol.RequestHolder, key string) ProcessedResponse {
	c.mu.Lock()
	subs := c.subs[key]
	delete(c.subs, key)
	c.mu.Unlock()

	wrappers := make(chan *protocol.ResponseHolderWrapper, len(subs))
	for _, sub := range subs {
		sub.cancel()
		wrappers <- &protocol.ResponseHolderWrapper{
			UpstreamId: NoUpstream,
			RequestId:  request.Id(),
			Response:   protocol.NewChannelSubscriptionEventResponse(request.Id(), "xrpc.ch.close", nil, sub.channelId),
		}
	}
	close(wrappers)
	return &SubscriptionResponse{wrappers}
}

func (c *channelSubCtx) Exists(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.subs[key]
	return ok
}

// addSub allocates the next channel id of this connection, files the
// subscription under the client's request id and returns the channel id.
func (c *channelSubCtx) addSub(requestId string, cancel context.CancelFunc) uint64 {
	channelId := c.channelIds.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs[requestId] = append(c.subs[requestId], channelSub{channelId: channelId, cancel: cancel})
	return channelId
}

func newChannelSubCtx() *channelSubCtx {
	return &channelSubCtx{subs: make(map[string][]channelSub)}
}

var _ SubCtx = (*channelSubCtx)(nil)
