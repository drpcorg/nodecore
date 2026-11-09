package blocks

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
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
		lastPublished:        utils.NewAtomic[protocol.Block](),
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
		lastPublished:        utils.NewAtomic[protocol.Block](),
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

// scriptedValidator answers the results in order, repeats the last one, counts calls, and
// can be held open to simulate a slow check.
type scriptedValidator struct {
	results []validations.ValidationSettingResult
	calls   atomic.Int32
	hold    chan struct{} // nil: answer at once; otherwise answer when closed
}

func (s *scriptedValidator) Validate(ctx context.Context) validations.ValidationSettingResult {
	call := int(s.calls.Add(1)) - 1
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-ctx.Done():
			return validations.UnknownResult
		}
	}
	if call >= len(s.results) {
		call = len(s.results) - 1
	}
	return s.results[call]
}

func newGuardedProcessor(validator validations.SettingsValidator) (*GenericHeadProcessor, *stubHead) {
	head := &stubHead{heads: make(chan protocol.Block)}
	processor := &GenericHeadProcessor{
		upstreamId:           "up",
		head:                 head,
		manualHeadChan:       make(chan protocol.Block, 100),
		lifecycle:            utils.NewGenericLifecycle("up_head_processor", context.Background()),
		headNoUpdatesTimeout: time.Minute,
		lastUpdate:           utils.NewAtomic[time.Time](),
		lastPublished:        utils.NewAtomic[protocol.Block](),
		subManager:           utils.NewSubscriptionManager[HeadEvent]("up_head_processor"),
		chainValidator:       validator,
	}
	return processor, head
}

func noHeadEvent(t *testing.T, events <-chan HeadEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected head event %T %v", event, event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGenericHeadProcessorPublishesAJumpOnTheRightChain(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.Valid}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(20011)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(1), validator.calls.Load())
}

func TestGenericHeadProcessorIgnoresJumpsWithinTheThreshold(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.FatalSettingError}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	// exactly the threshold: not suspicious
	head.heads <- protocol.NewBlockWithHeight(10010)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10010)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(0), validator.calls.Load())
	// one past it: suspicious
	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadFatalEvent{}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(1), validator.calls.Load())
}

func TestGenericHeadProcessorChecksADownwardJump(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.Valid}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(20011)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(1), validator.calls.Load())
}

func TestGenericHeadProcessorDropsAJumpWhenTheCheckIsInconclusive(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{
		validations.SettingsError, validations.SettingsError, validations.Valid,
	}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(20011)
	head.heads <- protocol.NewBlockWithHeight(20012)
	noHeadEvent(t, sub.Events)
	// the reference stayed at 10, so this one is checked again and passes
	head.heads <- protocol.NewBlockWithHeight(20013)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(20013)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(3), validator.calls.Load())
}

func TestGenericHeadProcessorDropsAJumpOnTheWrongChainAndPublishesFatal(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.FatalSettingError}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadFatalEvent{}, nextHeadEvent(t, sub.Events))
	noHeadEvent(t, sub.Events)
}

func TestGenericHeadProcessorSkipsTheCheckWithoutAValidator(t *testing.T) {
	processor, head := newGuardedProcessor(nil)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(20011)}, nextHeadEvent(t, sub.Events))
}

func TestGenericHeadProcessorManualHeadsBypassTheGuardAndMoveTheReference(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.FatalSettingError}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	processor.UpdateHead(30000, 0)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeights(30000, 0)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(0), validator.calls.Load())
	// the manual head is the new reference, so 30001 is no jump
	head.heads <- protocol.NewBlockWithHeight(30001)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(30001)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(0), validator.calls.Load())
}

func TestGenericHeadProcessorStopCancelsTheChainCheck(t *testing.T) {
	// the validator answers only when released or cancelled, like a connector call does
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.Valid}, hold: make(chan struct{})}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(20011)
	require.Eventually(t, func() bool { return validator.calls.Load() == 1 }, time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		processor.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stop did not cancel the chain check")
	}
	assert.Equal(t, HeadStateEvent{Running: false}, nextHeadEvent(t, sub.Events))
	// the validator was never released, so only cancellation can have let Stop through
	noHeadEvent(t, sub.Events)
}

func TestGenericHeadProcessorForgetsTheReferenceOnStart(t *testing.T) {
	validator := &scriptedValidator{results: []validations.ValidationSettingResult{validations.FatalSettingError}}
	processor, head := newGuardedProcessor(validator)
	sub := processor.Subscribe("test")
	defer sub.Unsubscribe()
	processor.Start()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))
	head.heads <- protocol.NewBlockWithHeight(10)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(10)}, nextHeadEvent(t, sub.Events))

	processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: false}, nextHeadEvent(t, sub.Events))
	processor.Start()
	defer processor.Stop()
	assert.Equal(t, HeadStateEvent{Running: true}, nextHeadEvent(t, sub.Events))

	head.heads <- protocol.NewBlockWithHeight(20011)
	assert.Equal(t, HeadBlockEvent{HeadData: protocol.NewBlockWithHeight(20011)}, nextHeadEvent(t, sub.Events))
	assert.Equal(t, int32(0), validator.calls.Load())
}
