package flow

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/flow/subengine"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func pendingTestUpConfig() *config.Upstream {
	return &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &chains.Options{InternalTimeout: 5 * time.Second},
	}
}

func pendingMethodsMock() *mocks.MethodsMock {
	m := mocks.NewMethodsMock()
	m.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_subscribe", "eth_getTransactionByHash"))
	m.On("HasMethod", mock.Anything).Return(true).Maybe()
	return m
}

// registerPendingUpstream publishes a state event giving the upstream WsCap +
// PendingTxCap, so the chain knows it can serve pending-tx subs.
func registerPendingUpstream(chSup upstreams.ChainSupervisor, id string, caps mapset.Set[protocol.Cap]) {
	state := protocol.DefaultUpstreamState(pendingMethodsMock(), caps, "idx", nil, nil)
	state.Status = protocol.Available
	chSup.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        id,
		EventType: &protocol.StateUpstreamEvent{State: &state},
	})
	time.Sleep(30 * time.Millisecond)
}

func pendingCaps() mapset.Set[protocol.Cap] {
	return mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.PendingTxCap)
}

// pendingNotif builds a ws subscription notification frame carrying a tx hash as
// its result (SubId set so it is treated as an event, not a confirmation).
func pendingNotif(hash string) *protocol.WsResponse {
	return &protocol.WsResponse{SubId: "0xsub", Message: []byte(`"` + hash + `"`)}
}

// wsUpstream builds an upstream whose only connector is a websocket connector that
// returns subResp from Subscribe; the channel feeds the merged source.
func wsUpstream(t *testing.T, subResp protocol.UpstreamSubscriptionResponse, subErr error) *mocks.ConnectorMock {
	t.Helper()
	conn := mocks.NewConnectorMockWithType(specs.WebsocketConnector)
	conn.On("Subscribe", mock.Anything, mock.Anything).Return(subResp, subErr)
	conn.On("Unsubscribe", mock.Anything).Maybe()
	return conn
}

func assertNoPendingEvent(t *testing.T, ch <-chan *protocol.WsResponse) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("unexpected extra event: %s", string(r.Message))
	case <-time.After(250 * time.Millisecond):
	}
}

// The shared source opens a sub on every capable upstream, merges them and emits
// each tx hash once even when several upstreams report the same hash.
func TestPendingTxSourceMergesAndDedupes(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor() // ARBITRUM
	registerPendingUpstream(chSup, "up1", pendingCaps())
	registerPendingUpstream(chSup, "up2", pendingCaps())

	ch1 := make(chan *protocol.WsResponse, 10)
	ch2 := make(chan *protocol.WsResponse, 10)
	conn1 := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch1, "op1"), nil)
	conn2 := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch2, "op2"), nil)
	up1 := test_utils.TestEvmUpstream(conn1, pendingTestUpConfig(), pendingMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(conn2, pendingTestUpConfig(), pendingMethodsMock(), nil)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()
	upSup.On("GetUpstream", "up2").Return(up2).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newPendingTxSourceBuilder(upSup, chains.ARBITRUM)(ctx)
	require.NoError(t, err)
	assert.Equal(t, pendingTxBufferSize, src.Buffer)

	ch1 <- pendingNotif("0xaaa") // same hash from both upstreams -> emitted once
	ch2 <- pendingNotif("0xaaa")
	ch2 <- pendingNotif("0xbbb")

	got1 := readWsResponse(t, src.Events)
	got2 := readWsResponse(t, src.Events)
	assert.ElementsMatch(t, []string{`"0xaaa"`, `"0xbbb"`}, []string{string(got1.Message), string(got2.Message)})
	assertNoPendingEvent(t, src.Events) // the duplicate 0xaaa was dropped
}

// One upstream failing to subscribe (or dropping) does not kill the merged source;
// the surviving upstream still delivers hashes.
func TestPendingTxSourceOneFeedFailsNonTerminal(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	registerPendingUpstream(chSup, "good", pendingCaps())
	registerPendingUpstream(chSup, "bad", pendingCaps())

	chGood := make(chan *protocol.WsResponse, 10)
	connGood := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(chGood, "opGood"), nil)
	connBad := wsUpstream(t, nil, assert.AnError) // Subscribe errors
	upGood := test_utils.TestEvmUpstream(connGood, pendingTestUpConfig(), pendingMethodsMock(), nil)
	upBad := test_utils.TestEvmUpstream(connBad, pendingTestUpConfig(), pendingMethodsMock(), nil)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "good").Return(upGood).Maybe()
	upSup.On("GetUpstream", "bad").Return(upBad).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newPendingTxSourceBuilder(upSup, chains.ARBITRUM)(ctx)
	require.NoError(t, err) // built with the one good feed

	chGood <- pendingNotif("0xabc")
	got := readWsResponse(t, src.Events)
	assert.Equal(t, `"0xabc"`, string(got.Message))
}

// No ws-capable upstream means the source cannot be built; the engine surfaces the
// error so the client falls back to the generic node-backed path.
func TestPendingTxSourceNoCapableUpstreams(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	// only WsCap, no PendingTxCap → skipped by the builder
	registerPendingUpstream(chSup, "up1", mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))

	conn := wsUpstream(t, nil, assert.AnError)
	up := test_utils.TestEvmUpstream(conn, pendingTestUpConfig(), pendingMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := newPendingTxSourceBuilder(upSup, chains.ARBITRUM)(ctx)
	require.Error(t, err)
}

// Losing PendingTxCap chain-wide terminates the source so clients fail over,
// matching the local newHeads/logs sources.
func TestPendingTxSourceTerminatesWhenCapLost(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	registerPendingUpstream(chSup, "up1", pendingCaps())

	ch1 := make(chan *protocol.WsResponse, 10)
	conn1 := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch1, "op1"), nil)
	up1 := test_utils.TestEvmUpstream(conn1, pendingTestUpConfig(), pendingMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newPendingTxSourceBuilder(upSup, chains.ARBITRUM)(ctx)
	require.NoError(t, err)

	// up1 stops advertising PendingTxCap -> the chain loses it -> source terminates.
	registerPendingUpstream(chSup, "up1", mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))

	r := readWsResponse(t, src.Events)
	require.NotNil(t, r.Error, "expected a terminal frame after PendingTxCap is lost")
}

// A feed dying at runtime is non-terminal while another survives, but once every
// feed has died the source terminates even though PendingTxCap is still present
// (the feeds are fixed at build time and never reconnect).
func TestPendingTxSourceTerminatesWhenAllFeedsDie(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	registerPendingUpstream(chSup, "up1", pendingCaps())
	registerPendingUpstream(chSup, "up2", pendingCaps())

	ch1 := make(chan *protocol.WsResponse, 10)
	ch2 := make(chan *protocol.WsResponse, 10)
	conn1 := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch1, "op1"), nil)
	conn2 := wsUpstream(t, protocol.NewJsonRpcWsUpstreamResponse(ch2, "op2"), nil)
	up1 := test_utils.TestEvmUpstream(conn1, pendingTestUpConfig(), pendingMethodsMock(), nil)
	up2 := test_utils.TestEvmUpstream(conn2, pendingTestUpConfig(), pendingMethodsMock(), nil)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()
	upSup.On("GetUpstream", "up2").Return(up2).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newPendingTxSourceBuilder(upSup, chains.ARBITRUM)(ctx)
	require.NoError(t, err)

	// Kill feed 1 (PendingTxCap untouched) - the survivor still delivers.
	close(ch1)
	ch2 <- pendingNotif("0xabc")
	got := readWsResponse(t, src.Events)
	require.Nil(t, got.Error)
	assert.Equal(t, `"0xabc"`, string(got.Message))

	// Kill feed 2 - no feeds left, so the source must terminate.
	close(ch2)
	r := readWsResponse(t, src.Events)
	require.NotNil(t, r.Error, "expected a terminal frame after every feed died")
}

// --- enrichPendingTx (drpc_pendingTransactions enrichment) ---------------

func txMethodsMock() *mocks.MethodsMock {
	m := mocks.NewMethodsMock()
	m.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getTransactionByHash"))
	m.On("HasMethod", "eth_getTransactionByHash").Return(true)
	m.On("HasMethod", mock.Anything).Return(false).Maybe()
	return m
}

func publishTxUpstream(chSup upstreams.ChainSupervisor, id string) {
	chSup.PublishUpstreamEvent(test_utils.CreateEvent(
		id,
		protocol.Available,
		protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("00")},
		txMethodsMock(),
	))
	time.Sleep(10 * time.Millisecond)
}

func enrichTestSetup(t *testing.T, sendResult protocol.ResponseHolder) (upstreams.ChainSupervisor, *mocks.UpstreamSupervisorMock) {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	publishTxUpstream(chSup, "up1")
	conn := mocks.NewConnectorMock() // json-rpc connector for eth_getTransactionByHash
	if sendResult != nil {
		conn.On("SendRequest", mock.Anything, mock.Anything).Return(sendResult)
	}
	up := test_utils.TestEvmUpstream(conn, pendingTestUpConfig(), txMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	return chSup, upSup
}

func TestEnrichPendingTxReturnsFullTransaction(t *testing.T) {
	txJSON := []byte(`{"hash":"0xaaa","from":"0x1","nonce":"0x2"}`)
	chSup, upSup := enrichTestSetup(t, protocol.NewSimpleHttpUpstreamResponse("1", txJSON, protocol.JsonRpc))

	tx, upstreamId := enrichPendingTx(context.Background(), upSup, chains.ARBITRUM, chSup, []byte(`"0xaaa"`))
	require.NotNil(t, tx)
	assert.JSONEq(t, string(txJSON), string(tx))
	assert.Equal(t, "id", upstreamId)
}

// The fix: a pending hash lives in only one node's mempool. Enrichment must
// broadcast and accept the upstream that actually has the tx, not drop it because
// the first-queried upstream returned null.
func TestEnrichPendingTxBroadcastsAndTakesNonNull(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	publishTxUpstream(chSup, "missing") // does not have the tx -> null
	publishTxUpstream(chSup, "has")     // has the tx

	txJSON := []byte(`{"hash":"0xaaa","from":"0x1"}`)
	connMissing := mocks.NewConnectorMock()
	connMissing.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte("null"), protocol.JsonRpc))
	connHas := mocks.NewConnectorMock()
	connHas.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewSimpleHttpUpstreamResponse("1", txJSON, protocol.JsonRpc))
	upMissing := test_utils.TestEvmUpstream(connMissing, pendingTestUpConfig(), txMethodsMock(), nil)
	upHas := test_utils.TestEvmUpstream(connHas, pendingTestUpConfig(), txMethodsMock(), nil)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "missing").Return(upMissing).Maybe()
	upSup.On("GetUpstream", "has").Return(upHas).Maybe()

	tx, _ := enrichPendingTx(context.Background(), upSup, chains.ARBITRUM, chSup, []byte(`"0xaaa"`))
	require.NotNil(t, tx)
	assert.JSONEq(t, string(txJSON), string(tx))
}

func TestEnrichPendingTxAllNullSkipped(t *testing.T) {
	chSup, upSup := enrichTestSetup(t, protocol.NewSimpleHttpUpstreamResponse("1", []byte("null"), protocol.JsonRpc))

	tx, upstreamId := enrichPendingTx(context.Background(), upSup, chains.ARBITRUM, chSup, []byte(`"0xaaa"`))
	assert.Nil(t, tx)
	assert.Empty(t, upstreamId)
}

func TestEnrichPendingTxErrorSkipped(t *testing.T) {
	chSup, upSup := enrichTestSetup(t, protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc))

	tx, _ := enrichPendingTx(context.Background(), upSup, chains.ARBITRUM, chSup, []byte(`"0xaaa"`))
	assert.Nil(t, tx)
}

func TestEnrichPendingTxBadHashSkipped(t *testing.T) {
	chSup, upSup := enrichTestSetup(t, nil)

	tx, _ := enrichPendingTx(context.Background(), upSup, chains.ARBITRUM, chSup, []byte(`not-a-json-string`))
	assert.Nil(t, tx)
}

func TestIsNullResult(t *testing.T) {
	assert.True(t, isNullResult(nil))
	assert.True(t, isNullResult([]byte("null")))
	assert.True(t, isNullResult([]byte("  null  ")))
	assert.False(t, isNullResult([]byte(`{"hash":"0x1"}`)))
}

// --- newDrpcPendingTxSourceBuilder (end-to-end via a real engine) ---------

// pendingDrpcUpstream builds an upstream with BOTH a websocket connector (for the
// shared newPendingTransactions sub, feeding wsCh) and a json-rpc connector (for
// the eth_getTransactionByHash enrichment, returning txResult).
func pendingDrpcUpstream(id string, wsCh chan *protocol.WsResponse, opId string, txResult protocol.ResponseHolder) *upstreams.BaseUpstream {
	wsConn := mocks.NewConnectorMockWithType(specs.WebsocketConnector)
	wsConn.On("Subscribe", mock.Anything, mock.Anything).Return(protocol.NewJsonRpcWsUpstreamResponse(wsCh, opId), nil)
	wsConn.On("Unsubscribe", mock.Anything).Maybe()

	jsonConn := mocks.NewConnectorMock() // json-rpc connector
	jsonConn.On("SendRequest", mock.Anything, mock.Anything).Return(txResult)

	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.DefaultUpstreamState(pendingMethodsMock(), mapset.NewThreadUnsafeSet[protocol.Cap](), "idx", nil, nil))
	return upstreams.NewBaseUpstreamWithParams(
		id,
		chains.ARBITRUM,
		[]connectors.ApiConnector{wsConn, jsonConn},
		pendingTestUpConfig(),
		"idx",
		upState,
		nil,
		nil,
		nil,
	)
}

// A drpc client gets the shared pending hash, enriched into a full tx object. The
// source rides the same localPendingTxKey source (one node sub set for both topics).
func TestDrpcPendingTxSourceEnrichesHashToTransaction(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor() // ARBITRUM
	registerPendingUpstream(chSup, "up1", pendingCaps())

	txJSON := []byte(`{"hash":"0xaaa","from":"0x1","nonce":"0x2"}`)
	wsCh := make(chan *protocol.WsResponse, 10)
	up1 := pendingDrpcUpstream("up1", wsCh, "op1", protocol.NewSimpleHttpUpstreamResponse("1", txJSON, protocol.JsonRpc))

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := subengine.NewEngine(ctx, chains.ARBITRUM)

	src, err := newDrpcPendingTxSourceBuilder(upSup, chains.ARBITRUM, engine)(ctx)
	require.NoError(t, err)
	assert.Equal(t, pendingTxBufferSize, src.Buffer)

	wsCh <- pendingNotif("0xaaa") // shared source emits the hash -> drpc enriches it

	got := readWsResponse(t, src.Events)
	require.Nil(t, got.Error)
	assert.JSONEq(t, string(txJSON), string(got.Message))
	assert.Equal(t, "up1", got.UpstreamId)
}

// When the shared hash source terminates (PendingTxCap lost), the drpc source
// propagates that terminal cause to its clients rather than stalling.
func TestDrpcPendingTxSourcePropagatesTerminal(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor()
	registerPendingUpstream(chSup, "up1", pendingCaps())

	wsCh := make(chan *protocol.WsResponse, 10)
	up1 := pendingDrpcUpstream("up1", wsCh, "op1", protocol.NewSimpleHttpUpstreamResponse("1", []byte("null"), protocol.JsonRpc))

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := subengine.NewEngine(ctx, chains.ARBITRUM)

	src, err := newDrpcPendingTxSourceBuilder(upSup, chains.ARBITRUM, engine)(ctx)
	require.NoError(t, err)

	// Drop PendingTxCap -> shared source terminates -> drpc propagates the terminal.
	registerPendingUpstream(chSup, "up1", mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))

	r := readWsResponse(t, src.Events)
	require.NotNil(t, r.Error, "expected drpc source to propagate the shared source's terminal frame")
}

// Enrichment must run with bounded concurrency, not serially: a slow
// eth_getTransactionByHash must not block the read side of the shared hash stream
// (a serial drain would let the inner subscription buffer fill and get this source
// disconnected as too slow). We hold every enrichment in-flight simultaneously and
// assert the peak concurrency reaches the number of pending hashes - a serial drain
// would peak at 1.
func TestDrpcPendingTxSourceEnrichesConcurrently(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
	chSup := test_utils.CreateChainSupervisor() // ARBITRUM
	registerPendingUpstream(chSup, "up1", pendingCaps())

	const n = 8 // fewer than pendingEnrichConcurrency, so all can run at once
	txJSON := []byte(`{"hash":"0xaaa"}`)

	var inflight, maxInflight atomic.Int32
	release := make(chan struct{})

	wsCh := make(chan *protocol.WsResponse, n)
	wsConn := mocks.NewConnectorMockWithType(specs.WebsocketConnector)
	wsConn.On("Subscribe", mock.Anything, mock.Anything).Return(protocol.NewJsonRpcWsUpstreamResponse(wsCh, "op1"), nil)
	wsConn.On("Unsubscribe", mock.Anything).Maybe()

	// Every enrichment blocks until release is closed, so they pile up concurrently;
	// each call records the running peak.
	jsonConn := mocks.NewConnectorMock()
	jsonConn.On("SendRequest", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			cur := inflight.Add(1)
			for {
				m := maxInflight.Load()
				if cur <= m || maxInflight.CompareAndSwap(m, cur) {
					break
				}
			}
			<-release
			inflight.Add(-1)
		}).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", txJSON, protocol.JsonRpc))

	upState := utils.NewAtomic[protocol.UpstreamState]()
	upState.Store(protocol.DefaultUpstreamState(pendingMethodsMock(), mapset.NewThreadUnsafeSet[protocol.Cap](), "idx", nil, nil))
	up1 := upstreams.NewBaseUpstreamWithParams(
		"up1",
		chains.ARBITRUM,
		[]connectors.ApiConnector{wsConn, jsonConn},
		pendingTestUpConfig(),
		"idx",
		upState,
		nil,
		nil,
		nil,
	)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "up1").Return(up1).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := subengine.NewEngine(ctx, chains.ARBITRUM)

	src, err := newDrpcPendingTxSourceBuilder(upSup, chains.ARBITRUM, engine)(ctx)
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		wsCh <- pendingNotif(fmt.Sprintf("0x%02x", i)) // distinct hashes so none are deduped
	}

	// All n enrichments must be in-flight at once; a serial drain would never exceed 1.
	require.Eventually(t, func() bool { return inflight.Load() == int32(n) }, time.Second, 5*time.Millisecond,
		"enrichments must run concurrently; a serial drain blocks on the first one")
	close(release)

	for i := 0; i < n; i++ {
		got := readWsResponse(t, src.Events)
		require.Nil(t, got.Error)
		assert.JSONEq(t, string(txJSON), string(got.Message))
	}
	assert.GreaterOrEqual(t, maxInflight.Load(), int32(n), "enrichment did not run concurrently")
}
