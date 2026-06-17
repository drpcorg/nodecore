package flow

import (
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
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
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
	m.On("GetSupportedMethods").Return(mapset.NewThreadUnsafeSet[string]("eth_getLogs"))
	m.On("HasMethod", "eth_getLogs").Return(true)
	m.On("HasMethod", mock.Anything).Return(false).Maybe()
	return m
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
	require.NoError(t, specs.NewMethodSpecLoader().Load())

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
	logs, upstreamId := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block)

	require.Len(t, logs, 1)
	assert.Equal(t, "id", upstreamId) // BaseUpstream id from TestEvmUpstream
	connLow.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// A block with no upstream at its height is skipped (nil), not a terminal error.
func TestFetchBlockLogsNoUpstreamAtHeight(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	chSup := test_utils.CreateChainSupervisor()
	publishLogsUpstream(chSup, "low", 50)

	conn := mocks.NewConnectorMock()
	up := test_utils.TestEvmUpstream(conn, logsTestUpConfig(), logsMethodsMock(), nil)
	upSup := mocks.NewUpstreamSupervisorMock()
	upSup.On("GetChainSupervisor", chains.ARBITRUM).Return(chSup)
	upSup.On("GetUpstream", "low").Return(up).Maybe()

	block := protocol.Block{Height: 100, Hash: blockchain.NewHashIdFromString("aa")}
	logs, upstreamId := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block)

	assert.Nil(t, logs)
	assert.Empty(t, upstreamId)
	conn.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

// An upstream that errors on eth_getLogs causes the block to be skipped (nil)
// after the bounded walk-down, not a terminal failure.
func TestFetchBlockLogsErrorSkipsBlock(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())

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
	logs, _ := fetchBlockLogs(context.Background(), upSup, chains.ARBITRUM, chSup, newLogsTestRegistry(upSup), block)

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

func readWsResponse(t *testing.T, ch <-chan *protocol.WsResponse) *protocol.WsResponse {
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

func assertRemoved(t *testing.T, r *protocol.WsResponse, want bool) {
	t.Helper()
	require.NotNil(t, r)
	require.Nil(t, r.Error, "unexpected terminal frame")
	var m map[string]any
	require.NoError(t, sonic.Unmarshal(r.Message, &m))
	removed, _ := m["removed"].(bool)
	assert.Equal(t, want, removed)
}

func logsSourceTestSetup(t *testing.T, sendResult protocol.ResponseHolder) (upstreams.ChainSupervisor, *mocks.UpstreamSupervisorMock, *rating.RatingRegistry) {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())
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
// the new block's logs with removed:false.
func TestLogsSourceReorgReemitsRemoved(t *testing.T) {
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
	assertRemoved(t, readWsResponse(t, src.Events), false) // block 100 logs

	// Height 101 builds on a different 100 (parent "bad") -> reorg: drop 100, new 101.
	publishHead(chSup, "up1", 101, "a1", "de")
	assertRemoved(t, readWsResponse(t, src.Events), true)  // block 100 re-emitted as removed
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
	require.NotNil(t, r.Error, "expected a terminal error frame when LogsCap is absent")
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
	require.NotNil(t, r.Error, "expected a terminal frame after LogsCap is lost")
}

// An eth_getLogs failure for one block skips it without terminating the source;
// the next block's logs are still delivered.
func TestLogsSourceSkipsBlockOnGetLogsError(t *testing.T) {
	require.NoError(t, specs.NewMethodSpecLoader().Load())
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
	publishHead(chSup, "up1", 100, "a0", "99") // getLogs errors -> skipped
	publishHead(chSup, "up1", 101, "a1", "a0") // getLogs ok -> emitted

	// The only event is block 101's log (block 100 produced none) and the source is alive.
	assertRemoved(t, readWsResponse(t, src.Events), false)
}

func TestLogCacheFIFO(t *testing.T) {
	c := newLogCache(2)
	c.put("a", []json.RawMessage{[]byte(`1`)})
	c.put("b", []json.RawMessage{[]byte(`2`)})
	c.put("c", []json.RawMessage{[]byte(`3`)}) // evicts "a"

	_, ok := c.get("a")
	assert.False(t, ok, "oldest entry should be evicted")
	got, ok := c.get("b")
	assert.True(t, ok)
	assert.Equal(t, json.RawMessage(`2`), got[0])
	_, ok = c.get("c")
	assert.True(t, ok)

	// overwriting an existing key does not grow/evict
	c.put("b", []json.RawMessage{[]byte(`22`)})
	got, _ = c.get("b")
	assert.Equal(t, json.RawMessage(`22`), got[0])
	_, ok = c.get("c")
	assert.True(t, ok, "overwrite must not evict another entry")
}
