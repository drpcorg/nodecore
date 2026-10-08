package upstreams_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/dimensions"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const slowRequestDelay = 700 * time.Millisecond

// newEvmNode is a JSON-RPC node that answers just enough for an EVM upstream to
// be validated and to follow its head. eth_getBalance is answered slowly.
func newEvmNode(t *testing.T, chainId uint64) *httptest.Server {
	t.Helper()
	return newEvmNodeWithWs(t, chainId, &atomic.Int32{})
}

type rpcRequest struct {
	Id     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

// serveEvmWs answers JSON-RPC over a websocket and feeds every eth_subscribe
// with new heads until the connection is closed.
func serveEvmWs(
	w http.ResponseWriter,
	r *http.Request,
	connections *atomic.Int32,
	reply func(request rpcRequest) map[string]any,
	block func() map[string]any,
) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	connections.Add(1)
	defer connections.Add(-1)
	defer func() { _ = conn.Close() }()

	var writeMu sync.Mutex
	write := func(message any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(message)
	}
	closed := make(chan struct{})
	defer close(closed)

	for {
		var request rpcRequest
		if err := conn.ReadJSON(&request); err != nil {
			return
		}
		if request.Method != "eth_subscribe" {
			if err := write(reply(request)); err != nil {
				return
			}
			continue
		}
		if err := write(map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": "0xsub"}); err != nil {
			return
		}
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-closed:
					return
				case <-ticker.C:
					head := map[string]any{"jsonrpc": "2.0", "method": "eth_subscription", "params": map[string]any{"subscription": "0xsub", "result": block()}}
					if err := write(head); err != nil {
						return
					}
				}
			}
		}()
	}
}

func newEvmNodeWithWs(t *testing.T, chainId uint64, wsConnections *atomic.Int32) *httptest.Server {
	t.Helper()
	started := time.Now()
	block := func() map[string]any {
		number := 1000 + uint64(time.Since(started)/(100*time.Millisecond))
		return map[string]any{
			"number":     fmt.Sprintf("0x%x", number),
			"hash":       fmt.Sprintf("0x%064x", number),
			"parentHash": fmt.Sprintf("0x%064x", number-1),
			"timestamp":  fmt.Sprintf("0x%x", time.Now().Unix()),
		}
	}
	answer := func(method string) any {
		switch method {
		case "eth_chainId":
			return fmt.Sprintf("0x%x", chainId)
		case "net_version":
			return fmt.Sprintf("%d", chainId)
		case "eth_syncing":
			return false
		case "net_peerCount":
			return "0x20"
		case "web3_clientVersion":
			return "Geth/v1.16.0/linux-amd64/go1.24"
		case "eth_blockNumber":
			return block()["number"]
		case "eth_getBlockByNumber", "eth_getBlockByHash":
			return block()
		case "eth_getBalance":
			time.Sleep(slowRequestDelay)
			return "0x1"
		}
		return nil
	}
	reply := func(request rpcRequest) map[string]any {
		return map[string]any{"jsonrpc": "2.0", "id": request.Id, "result": answer(request.Method)}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			serveEvmWs(w, r, wsConnections, reply, block)
			return
		}
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
			var batch []rpcRequest
			_ = json.Unmarshal(raw, &batch)
			replies := make([]map[string]any, 0, len(batch))
			for _, request := range batch {
				replies = append(replies, reply(request))
			}
			_ = json.NewEncoder(w).Encode(replies)
			return
		}
		var request rpcRequest
		_ = json.Unmarshal(raw, &request)
		_ = json.NewEncoder(w).Encode(reply(request))
	}))
	t.Cleanup(server.Close)
	return server
}

type noStats struct{}

func (noStats) AddRequestResults([]protocol.RequestResult) {}

type reloadUpstream struct {
	id     string
	chain  string
	url    string
	labels string
	// ws adds a websocket connector to the same node
	ws bool
	// grpc adds a grpc connector to this address; the head stays on json-rpc
	grpc string
}

// reloadAppConfig goes through the config parser, so the upstreams carry the same
// defaults they have in a config file.
func reloadAppConfig(t *testing.T, ups ...reloadUpstream) *config.AppConfig {
	t.Helper()
	// connector validation consults the method specs
	specs_utils.LoadMethodSpecs()
	var file strings.Builder
	file.WriteString("upstream-config:\n  upstreams:\n")
	for _, up := range ups {
		fmt.Fprintf(&file, `    - id: %s
      chain: %s
      poll-interval: 100ms
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        disable-methods-detection: true
        validate-syncing: false
        validate-peers: false
      connectors:
        - type: json-rpc
          url: %s
`, up.id, up.chain, up.url)
		if up.ws {
			fmt.Fprintf(&file, "        - type: websocket\n          url: %s\n", strings.Replace(up.url, "http://", "ws://", 1))
		}
		if up.grpc != "" {
			fmt.Fprintf(&file, "        - type: grpc\n          url: http://%s\n      head-connector: json-rpc\n", up.grpc)
		}
		if up.labels != "" {
			fmt.Fprintf(&file, "      labels:\n        %s\n", up.labels)
		}
	}
	appConfig, err := config.ParseAppConfig([]byte(file.String()))
	require.NoError(t, err)
	return appConfig
}

func startReloadSupervisor(t *testing.T, appConfig *config.AppConfig) upstreams.UpstreamSupervisor {
	t.Helper()
	specs_utils.LoadMethodSpecs()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	supervisor := upstreams.NewGenericUpstreamSupervisor(
		ctx,
		appConfig.UpstreamConfig,
		dimensions.NewGenericDimensionTracker(),
		noStats{},
		nil,
		"",
	)
	supervisor.StartUpstreams()
	return supervisor
}

func chainUpstreamIds(supervisor upstreams.UpstreamSupervisor, chain chains.Chain) []string {
	chainSupervisor := supervisor.GetChainSupervisor(chain)
	if chainSupervisor == nil {
		return nil
	}
	return chainSupervisor.GetUpstreamIds()
}

func chainAvailable(supervisor upstreams.UpstreamSupervisor, chain chains.Chain) bool {
	chainSupervisor := supervisor.GetChainSupervisor(chain)
	if chainSupervisor == nil {
		return false
	}
	state := chainSupervisor.GetChainState()
	return state.Status == protocol.Available && !state.HeadData.IsEmpty()
}

func waitFor(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	require.Eventually(t, condition, 15*time.Second, 10*time.Millisecond, msg)
}

func TestApplyUpstreamsAddsAndRemovesChain(t *testing.T) {
	ethNode, polygonNode := newEvmNode(t, 1), newEvmNode(t, 137)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: ethNode.URL}
	polygon := reloadUpstream{id: "polygon-1", chain: "polygon", url: polygonNode.URL}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")
	ethUpstream := supervisor.GetUpstream("eth-1")
	require.NotNil(t, ethUpstream)
	assert.Nil(t, supervisor.GetChainSupervisor(chains.POLYGON))

	chainEvents := supervisor.SubscribeChainSupervisor("test")
	defer chainEvents.Unsubscribe()

	// add an upstream of a chain that was not there
	diff, err := supervisor.ApplyUpstreams(reloadAppConfig(t, eth, polygon).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	assert.Equal(t, upstreams.UpstreamsDiff{Added: []string{"polygon-1"}, Removed: []string{}, Changed: []string{}}, diff)

	waitFor(t, func() bool { return chainAvailable(supervisor, chains.POLYGON) }, "polygon is not available")
	assert.Equal(t, []string{"polygon-1"}, chainUpstreamIds(supervisor, chains.POLYGON))
	select {
	case event := <-chainEvents.Events:
		added, ok := event.(*upstreams.AddChainSupervisorEvent)
		require.True(t, ok)
		assert.Equal(t, chains.POLYGON, added.ChainSupervisor.GetChain())
	case <-time.After(time.Second):
		t.Fatal("the new chain was not announced")
	}
	polygonUpstream := supervisor.GetUpstream("polygon-1")
	require.NotNil(t, polygonUpstream)
	assert.True(t, polygonUpstream.Running())

	// remove it again
	diff, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	assert.Equal(t, upstreams.UpstreamsDiff{Added: []string{}, Removed: []string{"polygon-1"}, Changed: []string{}}, diff)

	waitFor(t, func() bool { return supervisor.GetUpstream("polygon-1") == nil }, "polygon upstream is still registered")
	waitFor(t, func() bool { return !polygonUpstream.Running() }, "polygon upstream is still running")
	// the chain stays known, with nothing behind it
	polygonSupervisor := supervisor.GetChainSupervisor(chains.POLYGON)
	require.NotNil(t, polygonSupervisor)
	assert.Empty(t, polygonSupervisor.GetUpstreamIds())
	assert.Equal(t, protocol.Unavailable, polygonSupervisor.GetChainState().Status)

	// the other chain never noticed
	assert.Same(t, ethUpstream, supervisor.GetUpstream("eth-1"))
	assert.True(t, ethUpstream.Running())
	assert.True(t, chainAvailable(supervisor, chains.ETHEREUM))
	assert.Equal(t, []string{"eth-1"}, chainUpstreamIds(supervisor, chains.ETHEREUM))

	// and it can come back
	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth, polygon).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.POLYGON) }, "polygon didn't come back")
	assert.Same(t, polygonSupervisor, supervisor.GetChainSupervisor(chains.POLYGON))
	assert.Equal(t, polygonUpstream.GetHashIndex(), supervisor.GetUpstream("polygon-1").GetHashIndex())
}

func TestApplyUpstreamsWithinOneChain(t *testing.T) {
	firstNode, secondNode := newEvmNode(t, 1), newEvmNode(t, 1)
	first := reloadUpstream{id: "eth-1", chain: "ethereum", url: firstNode.URL}
	second := reloadUpstream{id: "eth-2", chain: "ethereum", url: secondNode.URL}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, first))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")

	_, err := supervisor.ApplyUpstreams(reloadAppConfig(t, first, second).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return len(chainUpstreamIds(supervisor, chains.ETHEREUM)) == 2 }, "the second upstream didn't join")

	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, second).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return supervisor.GetUpstream("eth-1") == nil }, "the first upstream is still registered")

	assert.Equal(t, []string{"eth-2"}, chainUpstreamIds(supervisor, chains.ETHEREUM))
	assert.True(t, chainAvailable(supervisor, chains.ETHEREUM))
}

func TestApplyUpstreamsReplacesChangedUpstream(t *testing.T) {
	node := newEvmNode(t, 1)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: node.URL}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")
	oldUpstream := supervisor.GetUpstream("eth-1")

	// the same list is not a change
	diff, err := supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	assert.True(t, diff.IsEmpty())
	assert.Same(t, oldUpstream, supervisor.GetUpstream("eth-1"))

	eth.labels = "region: eu"
	diff, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	assert.Equal(t, upstreams.UpstreamsDiff{Added: []string{}, Removed: []string{}, Changed: []string{"eth-1"}}, diff)

	waitFor(t, func() bool {
		up := supervisor.GetUpstream("eth-1")
		return up != nil && up != oldUpstream
	}, "the upstream was not replaced")
	newUpstream := supervisor.GetUpstream("eth-1")
	assert.False(t, oldUpstream.Running())
	// a sticky id handed out by the old instance still finds the upstream
	assert.Equal(t, oldUpstream.GetHashIndex(), newUpstream.GetHashIndex())

	// the removal of the old instance must not take the new one down with it
	waitFor(t, func() bool {
		state := supervisor.GetChainSupervisor(chains.ETHEREUM).GetUpstreamState("eth-1")
		if state == nil || state.Status != protocol.Available {
			return false
		}
		region, ok := state.Labels.GetLabel("region")
		return ok && region == "eu"
	}, "the replaced upstream didn't become available with its new config")
	assert.True(t, newUpstream.Running())
	// the upstream's state can land before its first head, and the chain is not available without one
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum didn't come back after the replace")
	assert.Equal(t, []string{"eth-1"}, chainUpstreamIds(supervisor, chains.ETHEREUM))
}

func TestApplyUpstreamsRejectedListChangesNothing(t *testing.T) {
	node := newEvmNode(t, 1)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: node.URL}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")
	ethUpstream := supervisor.GetUpstream("eth-1")

	valid := reloadAppConfig(t, eth, reloadUpstream{id: "polygon-1", chain: "polygon", url: node.URL}).UpstreamConfig.Upstreams
	duplicate := append([]*config.Upstream{}, valid...)
	duplicate = append(duplicate, valid[1])
	unknownChain := reloadAppConfig(t, eth, reloadUpstream{id: "polygon-1", chain: "polygon", url: node.URL}).UpstreamConfig.Upstreams
	unknownChain[1].ChainName = "no-such-chain"
	unknownBudget := reloadAppConfig(t, eth, reloadUpstream{id: "polygon-1", chain: "polygon", url: node.URL}).UpstreamConfig.Upstreams
	unknownBudget[1].RateLimitBudget = "budget"

	for name, list := range map[string][]*config.Upstream{
		"empty":          {},
		"duplicate id":   duplicate,
		"unknown chain":  unknownChain,
		"unknown budget": unknownBudget,
	} {
		t.Run(name, func(te *testing.T) {
			diff, err := supervisor.ApplyUpstreams(list)

			assert.Error(te, err)
			assert.True(te, diff.IsEmpty())
		})
	}

	// give a wrongly started upstream time to show up
	time.Sleep(300 * time.Millisecond)
	assert.Nil(t, supervisor.GetUpstream("polygon-1"))
	assert.Nil(t, supervisor.GetChainSupervisor(chains.POLYGON))
	assert.Same(t, ethUpstream, supervisor.GetUpstream("eth-1"))
	assert.True(t, ethUpstream.Running())
	assert.True(t, chainAvailable(supervisor, chains.ETHEREUM))
}

// The usual production shape: the head comes from a websocket subscription.
// Removing such an upstream must close its websocket and leave nothing running.
func TestApplyUpstreamsRemovesWebsocketUpstream(t *testing.T) {
	wsConnections := &atomic.Int32{}
	ethNode, polygonNode := newEvmNode(t, 1), newEvmNodeWithWs(t, 137, wsConnections)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: ethNode.URL}
	polygon := reloadUpstream{id: "polygon-ws", chain: "polygon", url: polygonNode.URL, ws: true}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")

	_, err := supervisor.ApplyUpstreams(reloadAppConfig(t, eth, polygon).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.POLYGON) }, "polygon is not available")
	waitFor(t, func() bool { return wsConnections.Load() == 1 }, "the websocket connector didn't connect")
	polygonUpstream := supervisor.GetUpstream("polygon-ws")
	require.NotNil(t, polygonUpstream)
	height := supervisor.GetChainSupervisor(chains.POLYGON).GetChainState().HeadData.Head.Height
	waitFor(t, func() bool {
		return supervisor.GetChainSupervisor(chains.POLYGON).GetChainState().HeadData.Head.Height > height
	}, "the head doesn't move")

	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)

	waitFor(t, func() bool { return supervisor.GetUpstream("polygon-ws") == nil }, "the upstream is still registered")
	waitFor(t, func() bool { return wsConnections.Load() == 0 }, "the websocket of the removed upstream is still open")
	assert.False(t, polygonUpstream.Running())
	assert.Empty(t, chainUpstreamIds(supervisor, chains.POLYGON))
	assert.True(t, chainAvailable(supervisor, chains.ETHEREUM))

	// it doesn't reconnect behind our back
	time.Sleep(time.Second)
	assert.Zero(t, wsConnections.Load())
}

// A request that is already on its way to an upstream over HTTP is not cut off
// when the upstream is removed.
func TestApplyUpstreamsLetsInFlightHttpRequestFinish(t *testing.T) {
	ethNode, polygonNode := newEvmNode(t, 1), newEvmNode(t, 137)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: ethNode.URL}
	polygon := reloadUpstream{id: "polygon-1", chain: "polygon", url: polygonNode.URL}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth, polygon))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.POLYGON) }, "polygon is not available")
	connector := supervisor.GetUpstream("polygon-1").GetConnector(specs.JsonRpcConnector)
	require.NotNil(t, connector)

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBalance", []any{"0x0000000000000000000000000000000000000001", "latest"}, chains.POLYGON)
	require.NoError(t, err)
	answered := make(chan protocol.ResponseHolder, 1)
	go func() { answered <- connector.SendRequest(context.Background(), request) }()
	time.Sleep(100 * time.Millisecond)

	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return supervisor.GetUpstream("polygon-1") == nil }, "the upstream is still registered")

	select {
	case <-answered:
		t.Fatal("the request was over before the upstream was removed, it proves nothing")
	default:
	}
	select {
	case response := <-answered:
		assert.False(t, response.HasError())
		assert.Equal(t, `"0x1"`, string(response.ResponseResult()))
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request never returned")
	}
}

const (
	grpcNodeSlowMethod   = "/test.Node/Slow"
	grpcNodeStreamMethod = "/test.Node/Stream"
)

// grpcNode is a gRPC node on a real TCP port that counts its connections: the
// ones it accepted so far and the ones that are open right now.
type grpcNode struct {
	addr     string
	accepted atomic.Int32
	open     atomic.Int32
}

type countingListener struct {
	net.Listener
	node *grpcNode
}

type countedConn struct {
	net.Conn
	node   *grpcNode
	closed sync.Once
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.node.accepted.Add(1)
	l.node.open.Add(1)
	return &countedConn{Conn: conn, node: l.node}, nil
}

func (c *countedConn) Close() error {
	c.closed.Do(func() { c.node.open.Add(-1) })
	return c.Conn.Close()
}

// newGrpcNode answers grpcNodeSlowMethod after slowRequestDelay and feeds
// grpcNodeStreamMethod with a frame every 20ms for as long as the stream lives.
func newGrpcNode(t *testing.T) *grpcNode {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	node := &grpcNode{addr: listener.Addr().String()}

	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		var request dshackle.Chain
		if err := stream.RecvMsg(&request); err != nil {
			return err
		}
		method, _ := grpc.MethodFromServerStream(stream)
		if method == grpcNodeStreamMethod {
			for height := uint64(1); ; height++ {
				if err := stream.SendMsg(&dshackle.ChainHead{Height: height}); err != nil {
					return err
				}
				select {
				case <-stream.Context().Done():
					return stream.Context().Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		if method == grpcNodeSlowMethod {
			time.Sleep(slowRequestDelay)
		}
		return stream.SendMsg(&dshackle.ChainHead{Height: 7})
	}))
	go func() { _ = server.Serve(&countingListener{Listener: listener, node: node}) }()
	t.Cleanup(server.Stop)
	return node
}

func grpcNodeRequest(t *testing.T, method string) protocol.RequestHolder {
	t.Helper()
	body, err := proto.Marshal(&dshackle.Chain{Type: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET})
	require.NoError(t, err)
	return protocol.NewInternalUpstreamGrpcRequest(method, body, chains.TRON)
}

// An upstream with a gRPC connector, in the shape of a Tron node: JSON-RPC for
// the head, gRPC next to it. Removing it must let a unary call that is already
// on the gRPC connection finish, end the streams on it, close the connection
// and never dial the node again.
func TestApplyUpstreamsRemovesGrpcUpstream(t *testing.T) {
	ethNode, tronNode, tronGrpc := newEvmNode(t, 1), newEvmNode(t, 0x2b6653dc), newGrpcNode(t)
	eth := reloadUpstream{id: "eth-1", chain: "ethereum", url: ethNode.URL}
	tron := reloadUpstream{id: "tron-grpc", chain: "tron", url: tronNode.URL, grpc: tronGrpc.addr}

	supervisor := startReloadSupervisor(t, reloadAppConfig(t, eth))
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.ETHEREUM) }, "ethereum is not available")
	ethUpstream := supervisor.GetUpstream("eth-1")

	_, err := supervisor.ApplyUpstreams(reloadAppConfig(t, eth, tron).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.TRON) }, "tron is not available")
	tronUpstream := supervisor.GetUpstream("tron-grpc")
	connector := tronUpstream.GetConnector(specs.GrpcConnector)
	require.NotNil(t, connector)

	// the chain serves over its gRPC connector, on one connection
	response := connector.SendRequest(context.Background(), grpcNodeRequest(t, "/test.Node/Fast"))
	require.False(t, response.HasError())
	assert.Equal(t, int32(1), tronGrpc.open.Load())

	stream, err := connector.Subscribe(context.Background(), grpcNodeRequest(t, grpcNodeStreamMethod))
	require.NoError(t, err)
	select {
	case frame := <-stream.ResponseChan():
		require.Nil(t, frame.GetError())
	case <-time.After(5 * time.Second):
		t.Fatal("the stream delivers nothing")
	}
	answered := make(chan protocol.ResponseHolder, 1)
	go func() {
		answered <- connector.SendRequest(context.Background(), grpcNodeRequest(t, grpcNodeSlowMethod))
	}()
	time.Sleep(100 * time.Millisecond)

	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth).UpstreamConfig.Upstreams)
	require.NoError(t, err)

	// the unary call that was in flight is answered, over a connection that is
	// still open at that moment
	select {
	case response := <-answered:
		assert.False(t, response.HasError())
		var head dshackle.ChainHead
		require.NoError(t, proto.Unmarshal(response.ResponseResult(), &head))
		assert.Equal(t, uint64(7), head.Height)
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight call never returned")
	}

	// the stream is ended with an error frame once the connection is closed
	var last protocol.SubResponse
	streamEnded := false
	for deadline := time.After(5 * time.Second); !streamEnded; {
		select {
		case frame, ok := <-stream.ResponseChan():
			if !ok {
				streamEnded = true
				break
			}
			last = frame
		case <-deadline:
			t.Fatal("the stream of the removed upstream is still open")
		}
	}
	require.NotNil(t, last)
	require.NotNil(t, last.GetError())
	assert.Contains(t, last.GetError().Message, "the client connection is closing")

	waitFor(t, func() bool { return supervisor.GetUpstream("tron-grpc") == nil }, "the upstream is still registered")
	waitFor(t, func() bool { return tronGrpc.open.Load() == 0 }, "the gRPC connection of the removed upstream is still open")
	assert.False(t, tronUpstream.Running())
	assert.Empty(t, chainUpstreamIds(supervisor, chains.TRON))

	// a call on the removed upstream's connector fails right away, and nothing
	// dials the node again
	response = connector.SendRequest(context.Background(), grpcNodeRequest(t, "/test.Node/Fast"))
	assert.True(t, response.HasError())
	accepted := tronGrpc.accepted.Load()
	time.Sleep(1500 * time.Millisecond)
	assert.Equal(t, accepted, tronGrpc.accepted.Load())
	assert.Zero(t, tronGrpc.open.Load())

	assert.Same(t, ethUpstream, supervisor.GetUpstream("eth-1"))
	assert.True(t, chainAvailable(supervisor, chains.ETHEREUM))

	// replacing a gRPC upstream closes the old connection and opens one new
	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth, tron).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool { return chainAvailable(supervisor, chains.TRON) }, "tron didn't come back")
	require.False(t, supervisor.GetUpstream("tron-grpc").GetConnector(specs.GrpcConnector).SendRequest(context.Background(), grpcNodeRequest(t, "/test.Node/Fast")).HasError())
	firstInstance := supervisor.GetUpstream("tron-grpc")

	tron.labels = "region: eu"
	_, err = supervisor.ApplyUpstreams(reloadAppConfig(t, eth, tron).UpstreamConfig.Upstreams)
	require.NoError(t, err)
	waitFor(t, func() bool {
		up := supervisor.GetUpstream("tron-grpc")
		return up != nil && up != firstInstance && chainAvailable(supervisor, chains.TRON)
	}, "the gRPC upstream was not replaced")
	require.False(t, supervisor.GetUpstream("tron-grpc").GetConnector(specs.GrpcConnector).SendRequest(context.Background(), grpcNodeRequest(t, "/test.Node/Fast")).HasError())
	waitFor(t, func() bool { return tronGrpc.open.Load() == 1 }, "the replaced gRPC upstream left its connection open")
}
