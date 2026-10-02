package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/rating"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func logsTestUpConfig() *config.Upstream {
	return &config.Upstream{
		Id:           "id",
		PollInterval: 10 * time.Millisecond,
		Options:      &chains.Options{InternalTimeout: 5 * time.Second},
	}
}

func logsMethodsMock() *mocks.MethodsMock {
	m := mocks.NewMethodsMock()
	m.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getLogs", "eth_getBlockByHash"))
	m.On("HasMethod", "eth_getLogs").Return(true)
	m.On("HasMethod", "eth_getBlockByHash").Return(true)
	m.On("HasMethod", mock.Anything).Return(false).Maybe()
	return m
}

// byMethod matches a SendRequest call by its JSON-RPC method.
func byMethod(method string) any {
	return mock.MatchedBy(func(r protocol.RequestHolder) bool { return r.Method() == method })
}

// byMethodAndParam matches a SendRequest call by method and a substring of its body.
func byMethodAndParam(method, param string) any {
	return mock.MatchedBy(func(r protocol.RequestHolder) bool {
		body, err := r.Body()
		return r.Method() == method && err == nil && bytes.Contains(body, []byte(param))
	})
}

// readWithin reads the next source event, failing after timeout.
func readWithin(t *testing.T, ch <-chan protocol.SubResponse, timeout time.Duration) protocol.SubResponse {
	t.Helper()
	select {
	case r, ok := <-ch:
		require.True(t, ok, "source channel closed unexpectedly")
		return r
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a source event")
		return nil
	}
}

// publishLogsUpstream registers an Available upstream at the given head height
// whose advertised methods include eth_getLogs.
func publishLogsUpstream(chSup interface {
	PublishUpstreamEvent(protocol.UpstreamEvent)
}, id string, height uint64) {
	chSup.PublishUpstreamEvent(test_utils.CreateEvent(
		id,
		protocol.Available,
		protocol.Block{Height: height, Hash: blockchain.NewHashIdFromString("00")},
		logsMethodsMock(),
	))
	time.Sleep(10 * time.Millisecond)
}

func newLogsTestRegistry(upSup *mocks.UpstreamSupervisorMock) *rating.RatingRegistry {
	return rating.NewRatingRegistry(upSup, nil, &config.ScorePolicyConfig{
		CalculationFunctionName: config.DefaultLatencyPolicyFuncName,
		CalculationInterval:     time.Minute,
	})
}

// fetchBlockLogs must pick an upstream whose head is at >= the block height (not
// the head producer): a too-low upstream is never queried.
func TestFetchBlockLogsSelectsByHeightAndParses(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor() // ARBITRUM
	publishLogsUpstream(chSup, "high", 100)
	publishLogsUpstream(chSup, "low", 50)

	connHigh := mocks.NewConnectorMock()
	connLow := mocks.NewConnectorMock()
	upHigh := test_utils.TestEvmUpstream(connHigh, logsTestUpConfig(), logsMethodsMock(), nil)
	upLow := test_utils.TestEvmUpstream(connLow, logsTestUpConfig(), logsMethodsMock(), nil)

	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "high").Return(upHigh).Maybe()
	upSup.On("GetUpstream", "low").Return(upLow).Maybe()

	logsJSON := []byte(`[{"address":"0xabc","topics":["0x1"],"removed":false}]`)
	connHigh.On("SendRequest", mock.Anything, mock.Anything).Return(protocol.NewSimpleHttpUpstreamResponse("1", logsJSON, protocol.JsonRpc))

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	logs, upstreamId, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(time.Second))

	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "id", upstreamId) // GenericUpstream id from TestEvmUpstream
	connLow.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// With no upstream at the block's height, fetchBlockLogs waits and then errors -
// the block is never skipped silently.
func TestFetchBlockLogsGivesUpWithoutUpstreamAtHeight(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	publishLogsUpstream(chSup, "low", 50)

	conn := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "low").Return(up).Maybe()

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	start := time.Now()
	logs, upstreamId, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(200*time.Millisecond))

	require.Error(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
	assert.Nil(t, logs)
	assert.Empty(t, upstreamId)
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// The case behind the fix: the head comes from an upstream without eth_getLogs,
// the one with it is a block behind. The block waits for it instead of being
// skipped, and the upstream without the method is never queried.
func TestFetchBlockLogsWaitsForUpstreamWithMethod(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	noLogs := mocks.NewMethodsMock()
	noLogs.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]())
	noLogs.On("HasMethod", mock.Anything).Return(false)

	chSup := test_utils.CreateChainSupervisor()
	chSup.PublishUpstreamEvent(test_utils.CreateEvent("fast", protocol.Available,
		protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("00")}, noLogs))
	publishLogsUpstream(chSup, "slow", 99)

	connFast := mocks.NewConnectorMock()
	connSlow := mocks.NewConnectorMock()
	upFast := test_utils.TestEvmUpstream(connFast, logsTestUpConfig(), noLogs, nil)
	upSlow := test_utils.TestEvmUpstream(connSlow, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "fast").Return(upFast).Maybe()
	upSup.On("GetUpstream", "slow").Return(upSlow).Maybe()
	connSlow.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"]}]`), protocol.JsonRpc))

	go func() {
		time.Sleep(150 * time.Millisecond)
		publishLogsUpstream(chSup, "slow", 100)
	}()

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	logs, _, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(3*time.Second))

	require.NoError(t, err)
	require.Len(t, logs, 1)
	connFast.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// An upstream error is retried rather than skipping the block.
func TestFetchBlockLogsRetriesUpstreamError(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	publishLogsUpstream(chSup, "high", 100)

	conn := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "high").Return(up).Maybe()

	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc)).Once()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"]}]`), protocol.JsonRpc))

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	logs, _, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(3*time.Second))

	require.NoError(t, err)
	require.Len(t, logs, 1)
	conn.AssertNumberOfCalls(t, "SendRequest", 2)
}

// A deadline already in the past still gets one attempt, and no waiting: a block
// queued behind a slow one does not add its own full wait.
func TestFetchBlockLogsPastDeadlineTriesOnce(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	publishLogsUpstream(chSup, "high", 100)

	conn := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "high").Return(up).Maybe()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc))

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	start := time.Now()
	_, _, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(-time.Second))

	require.Error(t, err)
	assert.Less(t, time.Since(start), 100*time.Millisecond)
	conn.AssertNumberOfCalls(t, "SendRequest", 1)
}

// An upstream that keeps erroring makes fetchBlockLogs error at the deadline.
func TestFetchBlockLogsGivesUpOnPersistentError(t *testing.T) {
	specs_utils.LoadMethodSpecs()

	chSup := test_utils.CreateChainSupervisor()
	publishLogsUpstream(chSup, "high", 100)

	conn := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "high").Return(up).Maybe()

	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc))

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	logs, _, err := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block, time.Now().Add(200*time.Millisecond))

	require.Error(t, err)
	assert.Nil(t, logs)
}

func TestSetRemovedTrue(t *testing.T) {
	out := setRemovedTrue([]byte(`{"address":"0xabc","removed":false,"topics":["0x1"]}`))
	var parsed map[string]any
	require.NoError(t, sonic.Unmarshal(out, &parsed))
	assert.Equal(t, true, parsed["removed"])
	assert.Equal(t, "0xabc", parsed["address"])
	assert.Len(t, parsed["topics"], 1)

	// adds the field when absent
	out = setRemovedTrue([]byte(`{"address":"0xabc"}`))
	require.NoError(t, sonic.Unmarshal(out, &parsed))
	assert.Equal(t, true, parsed["removed"])

	// invalid json is returned unchanged
	bad := []byte(`not json`)
	assert.Equal(t, bad, setRemovedTrue(bad))
}

// --- newLogsSourceBuilder (end-to-end source goroutine) ------------------

func logsCaps() mapset.Set[protocol.Cap] {
	return mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap, protocol.LogsCap)
}

// registerLogsUpstream publishes a StateUpstreamEvent so the chain supervisor
// knows the upstream's caps + methods + availability. State events set caps but
// do NOT drive the head stream - heads are advanced via publishHead.
func registerLogsUpstream(chSup upstreams.ChainSupervisor, id string, caps mapset.Set[protocol.Cap]) {
	state := protocol.DefaultUpstreamState(logsMethodsMock(), caps, "idx", nil, nil)
	state.Status = protocol.Available
	chSup.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id:        id,
		EventType: &protocol.StateUpstreamEvent{State: &state},
	})
	time.Sleep(30 * time.Millisecond)
}

// publishHead advances an upstream's head via a HeadUpstreamEvent, which is what
// triggers a HeadWrapper on the state stream (and refreshes the per-upstream head
// snapshot read by the height matcher).
func publishHead(chSup upstreams.ChainSupervisor, id string, height uint64, hash, parent string) {
	chSup.PublishUpstreamEvent(protocol.UpstreamEvent{
		Id: id,
		EventType: &protocol.HeadUpstreamEvent{
			Status: protocol.Available,
			Head: protocol.Block{
				Height:     height,
				Hash:       blockchain.NewHashIdFromString(hash),
				ParentHash: blockchain.NewHashIdFromString(parent),
			},
		},
	})
	time.Sleep(30 * time.Millisecond)
}

func readWsResponse(t *testing.T, ch <-chan protocol.SubResponse) protocol.SubResponse {
	t.Helper()
	select {
	case r, ok := <-ch:
		require.True(t, ok, "source channel closed unexpectedly")
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a source event")
		return nil
	}
}

func assertRemoved(t *testing.T, r protocol.SubResponse, want bool) {
	t.Helper()
	require.NotNil(t, r)
	require.Nil(t, r.GetError(), "unexpected terminal frame")
	var m map[string]any
	require.NoError(t, sonic.Unmarshal(r.GetMessage(), &m))
	removed, _ := m["removed"].(bool)
	assert.Equal(t, want, removed)
}

func logsSourceTestSetup(t *testing.T, sendResult protocol.ResponseHolder) (upstreams.ChainSupervisor, *mocks.UpstreamSupervisorMock, *rating.RatingRegistry) {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor() // ARBITRUM
	conn := mocks.NewConnectorMock()
	if sendResult != nil {
		conn.On("SendRequest", mock.Anything, mock.Anything).Return(sendResult)
	}
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	return chSup, upSup, newLogsTestRegistry(upSup)
}

// A new canonical block makes the source fetch its logs and fan each one out as a
// separate event with removed:false. Source.Buffer is the enlarged logs buffer.
func TestLogsSourceEmitsPerLog(t *testing.T) {
	twoLogs := protocol.NewSimpleHttpUpstreamResponse("1",
		[]byte(`[{"address":"0xa","topics":["0x1"],"removed":false},{"address":"0xb","topics":["0x2"],"removed":false}]`),
		protocol.JsonRpc)
	chSup, upSup, registry := logsSourceTestSetup(t, twoLogs)
	registerLogsUpstream(chSup, "up1", logsCaps()) // caps before build

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)
	assert.Equal(t, logsBufferSize, src.Buffer)

	time.Sleep(50 * time.Millisecond) // let StreamBlockUpdates subscribe
	publishHead(chSup, "up1", 100, "a0", "99")

	assertRemoved(t, readWsResponse(t, src.Events), false)
	assertRemoved(t, readWsResponse(t, src.Events), false)
}

// A reorg drops the orphaned block's cached logs with removed:true, then emits
// the new chain's logs with removed:false - including the reorged-in parent,
// fetched by hash since it never was a head.
func TestLogsSourceReorgReemitsRemoved(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, byMethod("eth_getBlockByHash")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"hash":"0xde","parentHash":"0x99","number":"0x64"}`), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, byMethod("eth_getLogs")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	registry := newLogsTestRegistry(upSup)
	registerLogsUpstream(chSup, "up1", logsCaps())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99")
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 100 logs

	// Height 101 builds on a different 100 ("de") -> reorg: drop a0, new de, new 101.
	publishHead(chSup, "up1", 101, "a1", "de")
	assertRemoved(t, readWsResponse(t, src.Events), true)  // a0 re-emitted as removed
	assertRemoved(t, readWsResponse(t, src.Events), false) // de logs
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 101 logs
}

// With no LogsCap on the chain, the source emits a terminal frame immediately so
// clients fall back to the generic node-backed path.
func TestLogsSourceTerminatesWhenLogsCapAbsentAtStart(t *testing.T) {
	chSup, upSup, registry := logsSourceTestSetup(t, nil)
	_ = chSup // no upstream published -> no LogsCap

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	r := readWsResponse(t, src.Events)
	require.NotNil(t, r.GetError(), "expected a terminal error frame when LogsCap is absent")
}

// Losing LogsCap mid-stream terminates the source on the next head update.
func TestLogsSourceTerminatesWhenLogsCapLost(t *testing.T) {
	oneLog := protocol.NewSimpleHttpUpstreamResponse("1",
		[]byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc)
	chSup, upSup, registry := logsSourceTestSetup(t, oneLog)
	registerLogsUpstream(chSup, "up1", logsCaps())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99")
	assertRemoved(t, readWsResponse(t, src.Events), false)

	// up1 stops advertising LogsCap; the next head update terminates the source.
	registerLogsUpstream(chSup, "up1", mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap))
	publishHead(chSup, "up1", 101, "a1", "a0")
	r := readWsResponse(t, src.Events)
	require.NotNil(t, r.GetError(), "expected a terminal frame after LogsCap is lost")
}

// An eth_getLogs failure for a block is retried: the block's logs are still
// delivered and the source stays alive.
func TestLogsSourceRetriesBlockOnGetLogsError(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc)).Once()
	conn.On("SendRequest", mock.Anything, mock.Anything).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	registry := newLogsTestRegistry(upSup)

	registerLogsUpstream(chSup, "up1", logsCaps())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99") // first getLogs errors, the retry succeeds

	assertRemoved(t, readWsResponse(t, src.Events), false)
	conn.AssertNumberOfCalls(t, "SendRequest", 2)
}

// The production case end to end: the merged head comes from an upstream with
// eth_getLogs disabled, the upstream that has it reaches the block a moment
// later. The block's logs are still delivered, from the slower upstream.
func TestLogsSourceWaitsForSlowerUpstreamWithMethod(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()

	noLogs := mocks.NewMethodsMock()
	noLogs.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getBlockByHash"))
	noLogs.On("HasMethod", "eth_getBlockByHash").Return(true)
	noLogs.On("HasMethod", mock.Anything).Return(false)
	fastState := protocol.DefaultUpstreamState(noLogs, mapset.NewThreadUnsafeSet[protocol.Cap](protocol.WsCap, protocol.NewHeadsCap), "idx", nil, nil)
	fastState.Status = protocol.Available
	chSup.PublishUpstreamEvent(protocol.UpstreamEvent{Id: "fast", EventType: &protocol.StateUpstreamEvent{State: &fastState}})
	registerLogsUpstream(chSup, "slow", logsCaps())

	connFast := mocks.NewConnectorMock()
	connSlow := mocks.NewConnectorMock()
	connSlow.On("SendRequest", mock.Anything, byMethod("eth_getLogs")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "fast").Return(test_utils.TestEvmUpstream(connFast, logsTestUpConfig(), noLogs, nil)).Maybe()
	upSup.On("GetUpstream", "slow").Return(test_utils.TestEvmUpstream(connSlow, logsTestUpConfig(), logsMethodsMock(), nil)).Maybe()
	registry := newLogsTestRegistry(upSup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "slow", 99, "99", "98")
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 99

	publishHead(chSup, "fast", 100, "a0", "99") // only the upstream without eth_getLogs has 100
	time.Sleep(150 * time.Millisecond)
	publishHead(chSup, "slow", 100, "a0", "99")
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 100, served by slow

	connFast.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// A block no upstream can serve in time is skipped; the source stays alive and
// delivers the next block.
func TestLogsSourceSkipsBlockThatCannotBeServed(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, byMethodAndParam("eth_getLogs", "0xa0")).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, byMethod("eth_getLogs")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	registry := newLogsTestRegistry(upSup)
	registerLogsUpstream(chSup, "up1", logsCaps())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99") // never served
	publishHead(chSup, "up1", 101, "a1", "a0")

	// the first event is block 101's log, after block 100 was given up on
	assertRemoved(t, readWithin(t, src.Events, logsGiveUp(chains.ARBITRUM)+3*time.Second), false)
}

// A head whose skipped ancestor cannot be fetched is still announced (with the
// gap) and the source stays alive.
func TestLogsSourceAnnouncesHeadWhenBackfillFails(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, byMethod("eth_getBlockByHash")).
		Return(protocol.NewTotalFailureFromErr("1", assert.AnError, protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, byMethodAndParam("eth_getLogs", "0xa2")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xb2","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, byMethod("eth_getLogs")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	registry := newLogsTestRegistry(upSup)
	registerLogsUpstream(chSup, "up1", logsCaps())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99")
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 100

	publishHead(chSup, "up1", 102, "a2", "a1") // 101 cannot be fetched
	r := readWithin(t, src.Events, logsGiveUp(chains.ARBITRUM)+3*time.Second)
	assertRemoved(t, r, false)
	assert.Contains(t, string(r.GetMessage()), "0xb2", "the next event is block 102's log")
}

// A head that jumps over a height gets that height backfilled by hash: its logs
// are delivered before the head's.
func TestLogsSourceBackfillsSkippedHeight(t *testing.T) {
	specs_utils.LoadMethodSpecs()
	chSup := test_utils.CreateChainSupervisor()
	conn := mocks.NewConnectorMock()
	conn.On("SendRequest", mock.Anything, byMethod("eth_getBlockByHash")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`{"hash":"0xa1","parentHash":"0xa0","number":"0x65"}`), protocol.JsonRpc))
	conn.On("SendRequest", mock.Anything, byMethod("eth_getLogs")).
		Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`[{"address":"0xa","topics":["0x1"],"removed":false}]`), protocol.JsonRpc))
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", mock.Anything).Return(up).Maybe()
	registry := newLogsTestRegistry(upSup)

	registerLogsUpstream(chSup, "up1", logsCaps())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := newLogsSourceBuilder(upSup, chains.ARBITRUM, registry)(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	publishHead(chSup, "up1", 100, "a0", "99")
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 100

	publishHead(chSup, "up1", 102, "a2", "a1")             // 101 was never a head
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 101, backfilled
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 102

	conn.AssertNumberOfCalls(t, "SendRequest", 4) // 3 x eth_getLogs + 1 x eth_getBlockByHash
}

func TestLogCacheFIFO(t *testing.T) {
	c := newLogCache(2)
	c.put("a", []*parsedLog{{raw: []byte(`1`)}})
	c.put("b", []*parsedLog{{raw: []byte(`2`)}})
	c.put("c", []*parsedLog{{raw: []byte(`3`)}}) // evicts "a"

	_, ok := c.get("a")
	assert.False(t, ok, "oldest entry should be evicted")
	got, ok := c.get("b")
	assert.True(t, ok)
	assert.Equal(t, json.RawMessage(`2`), got[0].raw)
	_, ok = c.get("c")
	assert.True(t, ok)

	// overwriting an existing key does not grow/evict
	c.put("b", []*parsedLog{{raw: []byte(`22`)}})
	got, _ = c.get("b")
	assert.Equal(t, json.RawMessage(`22`), got[0].raw)
	_, ok = c.get("c")
	assert.True(t, ok, "overwrite must not evict another entry")
}
