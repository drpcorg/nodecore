package blocks

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubConnector only reports its type; createHead never calls anything else
// (the test_utils mocks would import blocks - a cycle for an internal test).
type stubConnector struct {
	connectorType specs.ApiConnectorType
}

func (s *stubConnector) Start() {}

func (s *stubConnector) Stop() {}

func (s *stubConnector) Running() bool {
	return true
}

func (s *stubConnector) SendRequest(context.Context, protocol.RequestHolder) protocol.ResponseHolder {
	return nil
}

func (s *stubConnector) Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (s *stubConnector) Unsubscribe(string) {}

func (s *stubConnector) GetType() specs.ApiConnectorType {
	return s.connectorType
}

func (s *stubConnector) GetUrl() string {
	return ""
}

func (s *stubConnector) SubscribeStates(string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

// stubSpecific is a BlockChainSpecific whose SubscribeHeadRequest outcome is
// configurable; nothing else is exercised by createHead.
type stubSpecific struct {
	subErr error
}

func (s *stubSpecific) GetLatestBlock(context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) GetFinalizedBlock(context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) ParseBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) ParseSubscriptionBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	if s.subErr != nil {
		return nil, s.subErr
	}
	return protocol.NewInternalUpstreamGrpcRequest("/pkg.Service/Subscribe", nil, chains.SUI), nil
}

func TestCreateHead(t *testing.T) {
	options := &chains.Options{InternalTimeout: time.Second}
	tests := []struct {
		name          string
		connectorType specs.ApiConnectorType
		headMode      config.HeadMode
		subErr        error
		expected      Head
	}{
		{"json-rpc polls regardless of head-mode", specs.JsonRpcConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"rest polls", specs.RestConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"tendermint polls", specs.TendermintConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"websocket subscribes regardless of head-mode", specs.WebsocketConnector, config.HeadModePoll, nil, &SubscriptionHead{}},
		{"grpc poll mode", specs.GrpcConnector, config.HeadModePoll, nil, &RpcHead{}},
		{"grpc subscribe mode with support", specs.GrpcConnector, config.HeadModeSubscribe, nil, &SubscriptionHead{}},
		{"grpc subscribe mode without chain support falls back to polling", specs.GrpcConnector, config.HeadModeSubscribe, ErrUnsupportedHeadSubscriptions, &RpcHead{}},
		{"grpc subscribe mode with a real error stays a subscription head", specs.GrpcConnector, config.HeadModeSubscribe, errors.New("marshal failed"), &SubscriptionHead{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := &stubConnector{connectorType: tt.connectorType}
			head := createHead(t.Context(), "id", time.Second, tt.headMode, connector, &stubSpecific{subErr: tt.subErr}, options)
			assert.IsType(t, tt.expected, head)
		})
	}
}

func TestGenericHeadProcessorUpdateHeadDoesNotBlockWhenStopped(t *testing.T) {
	// a paused head processor has nobody draining manualHeadChan; the integrity
	// processor must never hang on it
	processor := &GenericHeadProcessor{manualHeadChan: make(chan protocol.Block, 100)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 150 {
			processor.UpdateHead(uint64(i), 0)
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UpdateHead blocked on a full manual head channel")
	}
}

// stubHead is a Head that does nothing; the processor tests only exercise the
// lifecycle around it.
type stubHead struct {
	heads chan protocol.Block
}

func (s *stubHead) Start() {}

func (s *stubHead) Stop() {}

func (s *stubHead) Running() bool {
	return true
}

func (s *stubHead) HeadsChan() chan protocol.Block {
	return s.heads
}

func (s *stubHead) OnNoHeadUpdates() {}

func (s *stubHead) GetCurrentBlock() protocol.Block {
	return protocol.ZeroBlock{}
}

func (s *stubHead) UpdateHead(protocol.Block) {}

func TestGenericHeadProcessorPublishesStateAroundBlocks(t *testing.T) {
	head := &stubHead{heads: make(chan protocol.Block)}
	processor := &GenericHeadProcessor{
		upstreamId:           "up",
		head:                 head,
		manualHeadChan:       make(chan protocol.Block, 100),
		lifecycle:            utils.NewGenericLifecycle("up_head_processor", context.Background()),
		headNoUpdatesTimeout: time.Minute,
		lastUpdate:           utils.NewAtomic[time.Time](),
		subManager:           utils.NewSubscriptionManager[HeadEvent]("up_head_processor"),
	}
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()

	processor.Start()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))

	processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: false}, nextHeadEvent(t, sub.Events))
}

func TestGenericHeadProcessorStopPublishesStateAfterTheLastBlock(t *testing.T) {
	// a syncing node floods heads, so a producer is always parked in its send when Stop runs
	head := &stubHead{heads: make(chan protocol.Block)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for height := uint64(1); ; height++ {
			select {
			case head.heads <- protocol.NewBlockWithHeight(height):
			case <-ctx.Done():
				return
			}
		}
	}()

	processor := &GenericHeadProcessor{
		upstreamId:           "up",
		head:                 head,
		manualHeadChan:       make(chan protocol.Block, 100),
		lifecycle:            utils.NewGenericLifecycle("up_head_processor", context.Background()),
		headNoUpdatesTimeout: time.Minute,
		lastUpdate:           utils.NewAtomic[time.Time](),
		subManager:           utils.NewSubscriptionManager[HeadEvent]("up_head_processor"),
	}

	for i := range 200 {
		processor.Start()
		processor.Stop()

		// a liveness consumer that subscribes during the pause must learn it is paused
		sub := processor.SubscribeWithReplay(fmt.Sprintf("late_%d", i))
		require.Equal(t, HeadStateEvent{Running: false}, nextHeadEvent(t, sub.Events), "iteration %d", i)
		sub.Unsubscribe()
	}
}

func nextHeadEvent(t *testing.T, events <-chan HeadEvent) HeadEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a head event")
		return nil
	}
}
