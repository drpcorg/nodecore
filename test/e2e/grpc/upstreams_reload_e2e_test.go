//go:build e2e

package grpc_e2e

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
	"github.com/drpcorg/public/pkg/dshackle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

const reloadSlowCallDelay = 3 * time.Second

// One gRPC connection and one SubscribeChainStatus stream live through the whole
// test while upstreams are added to and removed from the config file. Neither
// may notice anything but the chain events themselves.
func TestGrpcUpstreamsReloadKeepsConnection(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	ethNode := harness.StartMockNode(t, ctx, networkName, harness.MockNodeSpec{
		Alias: "mock-reload-eth", ChainId: 1, SlowMethod: "eth_getBalance", SlowDelay: reloadSlowCallDelay,
	})
	defer ethNode.Terminate(ctx)
	polygonNode := harness.StartMockNode(t, ctx, networkName, harness.MockNodeSpec{Alias: "mock-reload-polygon", ChainId: 137})
	defer polygonNode.Terminate(ctx)

	eth := reloadUpstream{id: "eth-upstream", chain: "ethereum", node: ethNode}
	polygon := reloadUpstream{id: "polygon-upstream", chain: "polygon", node: polygonNode}

	nodecore := harness.StartNodecore(t, ctx, networkName, reloadNodecoreConfig(eth))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	connectionLosses := watchConnection(ctx, conn)

	ethChain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	polygonChain := dshackle.ChainRef(chains.GetChain("polygon").GrpcId)
	polygonBalanceCall := harness.NativeCall{
		Chain: polygonChain, ID: 2, Method: "eth_getBalance", Payload: `["0x0000000000000000000000000000000000000001","latest"]`,
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	statuses := subscribeChainStatuses(t, streamCtx, client, nodecore)

	statuses.waitFor(t, "the full ethereum status", func(resp *dshackle.SubscribeChainStatusResponse) bool {
		return resp.GetChainDescription().GetChain() == ethChain && resp.GetFullResponse()
	})
	harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: ethChain, ID: 1, Method: "eth_blockNumber", Payload: `[]`}, 30*time.Second)
	assertNativeCallFails(t, ctx, client, nodecore, polygonChain)

	// add polygon while a request to ethereum is in flight
	slowCall := startSlowCall(ctx, client, ethChain)
	time.Sleep(500 * time.Millisecond)
	nodecore.WriteConfig(t, ctx, reloadNodecoreConfig(eth, polygon))

	statuses.waitFor(t, "the full polygon status", func(resp *dshackle.SubscribeChainStatusResponse) bool {
		return resp.GetChainDescription().GetChain() == polygonChain && resp.GetFullResponse() && chainAvailability(resp) == dshackle.AvailabilityEnum_AVAIL_OK
	})
	// the mock node reports its chain id as every balance
	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, polygonBalanceCall, 30*time.Second)
	if got := decodeJSONRPCStringResult(t, item.GetPayload()); got != "0x89" {
		t.Fatalf("the polygon request was not served by the polygon upstream: got %s want 0x89", got)
	}
	slowCall.assertSucceeded(t, nodecore)

	// remove polygon while another request to ethereum is in flight
	slowCall = startSlowCall(ctx, client, ethChain)
	time.Sleep(500 * time.Millisecond)
	nodecore.WriteConfig(t, ctx, reloadNodecoreConfig(eth))

	statuses.waitFor(t, "polygon becoming unavailable", func(resp *dshackle.SubscribeChainStatusResponse) bool {
		return resp.GetChainDescription().GetChain() == polygonChain && chainAvailability(resp) == dshackle.AvailabilityEnum_AVAIL_UNAVAILABLE
	})
	assertNativeCallFails(t, ctx, client, nodecore, polygonChain)
	slowCall.assertSucceeded(t, nodecore)

	// broken files are refused and nothing changes
	withPolygon := reloadNodecoreConfig(eth, polygon)
	for _, broken := range []struct {
		name   string
		config string
	}{
		{name: "not yaml", config: "upstream-config: ["},
		{name: "unknown chain", config: reloadNodecoreConfig(eth, reloadUpstream{id: "bad-upstream", chain: "no-such-chain", node: polygonNode})},
		{name: "duplicate id", config: reloadNodecoreConfig(eth, reloadUpstream{id: "eth-upstream", chain: "polygon", node: polygonNode})},
		{name: "no upstreams", config: reloadNodecoreConfig()},
		{name: "half-written file", config: withPolygon[:strings.Index(withPolygon, "chain: polygon")+len("chain: poly")]},
		{name: "invalid server setting", config: strings.Replace(withPolygon, "port: 8080", "port: -1", 1)},
	} {
		rejections := nodecore.LogCount(t, ctx, "is rejected")
		nodecore.WriteConfig(t, ctx, broken.config)
		waitForRejection(t, ctx, nodecore, broken.name, rejections)

		harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: ethChain, ID: 3, Method: "eth_blockNumber", Payload: `[]`}, 10*time.Second)
		assertNativeCallFails(t, ctx, client, nodecore, polygonChain)
	}

	// a valid file after the broken ones is applied again
	nodecore.WriteConfig(t, ctx, reloadNodecoreConfig(eth, polygon))
	statuses.waitFor(t, "polygon coming back", func(resp *dshackle.SubscribeChainStatusResponse) bool {
		return resp.GetChainDescription().GetChain() == polygonChain && chainAvailability(resp) == dshackle.AvailabilityEnum_AVAIL_OK
	})
	harness.NativeCallUntilSuccess(t, ctx, client, nodecore, polygonBalanceCall, 30*time.Second)

	if losses := connectionLosses.Load(); losses != 0 {
		t.Fatalf("the gRPC connection left READY %d times during the reloads\nlogs:\n%s", losses, nodecore.Logs(ctx))
	}
	statuses.assertAlive(t)
}

type reloadUpstream struct {
	id    string
	chain string
	node  *harness.RPCNode
}

func reloadNodecoreConfig(upstreams ...reloadUpstream) string {
	out := `server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
upstream-config:
  reload:
    watch-interval: 200ms
  upstreams:
`
	for _, upstream := range upstreams {
		out += fmt.Sprintf(`    - id: %s
      chain: %s
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.id, upstream.chain, upstream.node.InternalURL())
	}
	return out
}

// watchConnection counts how many times the connection stops being READY.
func watchConnection(ctx context.Context, conn *grpc.ClientConn) *atomic.Int32 {
	losses := &atomic.Int32{}
	go func() {
		state := conn.GetState()
		for conn.WaitForStateChange(ctx, state) {
			state = conn.GetState()
			if state != connectivity.Ready && state != connectivity.Shutdown {
				losses.Add(1)
			}
		}
	}()
	return losses
}

type chainStatusStream struct {
	responses chan *dshackle.SubscribeChainStatusResponse
	failure   chan error
	nodecore  *harness.Nodecore
}

func subscribeChainStatuses(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *harness.Nodecore) *chainStatusStream {
	t.Helper()
	stream, err := client.SubscribeChainStatus(ctx, &dshackle.SubscribeChainStatusRequest{})
	if err != nil {
		t.Fatalf("SubscribeChainStatus open stream failed: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}
	statuses := &chainStatusStream{
		responses: make(chan *dshackle.SubscribeChainStatusResponse, 10000),
		failure:   make(chan error, 1),
		nodecore:  nodecore,
	}
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				if ctx.Err() == nil {
					statuses.failure <- err
				}
				return
			}
			statuses.responses <- resp
		}
	}()
	return statuses
}

func (s *chainStatusStream) waitFor(t *testing.T, what string, match func(resp *dshackle.SubscribeChainStatusResponse) bool) {
	t.Helper()
	timeout := time.After(60 * time.Second)
	for {
		select {
		case resp := <-s.responses:
			if match(resp) {
				return
			}
		case err := <-s.failure:
			t.Fatalf("the chain status stream broke while waiting for %s: %v\nlogs:\n%s", what, err, s.nodecore.Logs(context.Background()))
		case <-timeout:
			t.Fatalf("the chain status stream didn't deliver %s\nlogs:\n%s", what, s.nodecore.Logs(context.Background()))
		}
	}
}

func (s *chainStatusStream) assertAlive(t *testing.T) {
	t.Helper()
	select {
	case err := <-s.failure:
		t.Fatalf("the chain status stream broke during the reloads: %v\nlogs:\n%s", err, s.nodecore.Logs(context.Background()))
	default:
	}
}

func chainAvailability(resp *dshackle.SubscribeChainStatusResponse) dshackle.AvailabilityEnum {
	for _, event := range resp.GetChainDescription().GetChainEvent() {
		if status := event.GetStatus(); status != nil {
			return status.GetAvailability()
		}
	}
	return dshackle.AvailabilityEnum_AVAIL_UNKNOWN
}

type slowCallResult struct {
	item    *dshackle.NativeCallReplyItem
	err     error
	elapsed time.Duration
}

type slowCall chan slowCallResult

// startSlowCall sends a request the mock node answers only after
// reloadSlowCallDelay, so that a reload can happen while it is in flight.
func startSlowCall(ctx context.Context, client dshackle.BlockchainClient, chain dshackle.ChainRef) slowCall {
	result := make(slowCall, 1)
	go func() {
		started := time.Now()
		item, err := harness.NativeCallOnce(ctx, client, harness.NativeCall{
			Chain: chain, ID: 100, Method: "eth_getBalance", Payload: `["0x0000000000000000000000000000000000000001","latest"]`,
		}, 30*time.Second)
		result <- slowCallResult{item: item, err: err, elapsed: time.Since(started)}
	}()
	return result
}

func (s slowCall) assertSucceeded(t *testing.T, nodecore *harness.Nodecore) {
	t.Helper()
	select {
	case result := <-s:
		if result.err != nil || !result.item.GetSucceed() {
			t.Fatalf("the in-flight request failed: err=%v item=%+v\nlogs:\n%s", result.err, result.item, nodecore.Logs(context.Background()))
		}
		if result.elapsed < reloadSlowCallDelay {
			t.Fatalf("the in-flight request returned after %s, before the upstream could answer it", result.elapsed)
		}
	case <-time.After(40 * time.Second):
		t.Fatalf("the in-flight request never returned\nlogs:\n%s", nodecore.Logs(context.Background()))
	}
}

func assertNativeCallFails(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *harness.Nodecore, chain dshackle.ChainRef) {
	t.Helper()
	// not eth_chainId: nodecore answers that one itself, without an upstream
	item, err := harness.NativeCallOnce(ctx, client, harness.NativeCall{Chain: chain, ID: 200, Method: "eth_gasPrice", Payload: `[]`}, 5*time.Second)
	if err == nil && item.GetSucceed() {
		t.Fatalf("a request to a chain without upstreams succeeded: %+v\nlogs:\n%s", item, nodecore.Logs(ctx))
	}
}

func waitForRejection(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, what string, rejectionsBefore int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if nodecore.LogCount(t, ctx, "is rejected") > rejectionsBefore {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nodecore didn't refuse the config (%s)\nlogs:\n%s", what, nodecore.Logs(ctx))
}
