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
// id when the ingress reads the subscribe frame (Reserve), the way a
// go-jsonrpc node does, so a cancel that follows in the same instant still
// finds them. A client may reuse one id for several subscribe calls, so a key
// holds a list.
type channelSubCtx struct {
	mu         sync.Mutex
	subs       map[string][]*channelSub
	channelIds atomic.Uint64
}

type channelSub struct {
	channelId uint64
	ctx       context.Context
	cancel    context.CancelFunc
	// attached is set once a processor runs on this reservation
	attached bool
}

// Reserve allocates the channel id and the subscription's own context, derived
// from the connection's, and files both under the client's request id.
func (c *channelSubCtx) Reserve(ctx context.Context, request protocol.RequestHolder) {
	realId, ok := request.(protocol.RealIdHolder)
	if !ok {
		return
	}
	subCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs[realId.RealId()] = append(c.subs[realId.RealId()], &channelSub{
		channelId: c.channelIds.Add(1),
		ctx:       subCtx,
		cancel:    cancel,
	})
}

func (c *channelSubCtx) Framing() subFraming {
	return &channelFraming{subCtx: c}
}

// Unsubscribe cancels every subscription filed under key, oldest first, and
// replies with one xrpc.ch.close [channelId] per closed channel, in that
// order; an unknown key gets nothing, as a go-jsonrpc node answers a cancel.
// A reservation no processor has attached yet stays filed, cancelled, until
// its processor attaches and finds it dead. The close is written by this
// reply, not by the subscription's own goroutine, so a value that arrived in
// the same instant as the cancel can reach the client after the close; a
// go-jsonrpc client drops a value for a channel it no longer knows.
func (c *channelSubCtx) Unsubscribe(request protocol.RequestHolder, key string) ProcessedResponse {
	c.mu.Lock()
	subs := c.subs[key]
	var pending []*channelSub
	for _, sub := range subs {
		if !sub.attached {
			pending = append(pending, sub)
		}
	}
	if len(pending) == 0 {
		delete(c.subs, key)
	} else {
		c.subs[key] = pending
	}
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

// attach hands the oldest not-yet-attached reservation under requestId to the
// processor that will serve it. A reservation the client already cancelled is
// handed out once (its context is done, the processor opens nothing) and
// dropped. ok is false when nothing was reserved.
func (c *channelSubCtx) attach(requestId string) (ctx context.Context, channelId uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	subs := c.subs[requestId]
	for i, sub := range subs {
		if sub.attached {
			continue
		}
		sub.attached = true
		if sub.ctx.Err() != nil {
			remaining := append(subs[:i:i], subs[i+1:]...)
			if len(remaining) == 0 {
				delete(c.subs, requestId)
			} else {
				c.subs[requestId] = remaining
			}
		}
		return sub.ctx, sub.channelId, true
	}
	return nil, 0, false
}

func newChannelSubCtx() *channelSubCtx {
	return &channelSubCtx{subs: make(map[string][]*channelSub)}
}

var _ SubCtx = (*channelSubCtx)(nil)
