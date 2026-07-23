//go:build e2e

package grpc_e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestGrpcNativeSubscribeLogsAcceptsObjectPayload(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-grpc-native-subscribe", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, grpcNativeSubscribeNodecoreConfig(upstream))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()

	chain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	status := harness.WaitForFullChainStatus(t, ctx, client, nodecore, chain, 45*time.Second)
	assertSubscriptionAdvertised(t, status, "logs")
	harness.ClearHardhatRequests(t, ctx, upstream)

	filterAddress := "0x0000000000000000000000000000000000000000"
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.NativeSubscribe(subCtx, &dshackle.NativeSubscribeRequest{
		Chain:   chain,
		Method:  "logs",
		Payload: []byte(fmt.Sprintf(`{"address":%q,"topics":[]}`, filterAddress)),
		// Force the generic node-backed subscribe path so this test keeps verifying
		// the dshackle NativeSubscribe(Method="logs", Payload=object) ->
		// eth_subscribe ["logs", object] mapping. Without an effective selector,
		// logs subscriptions are served by the local logs source and do not issue an
		// upstream eth_subscribe("logs", ...).
		Selector: latestBlockSelector(),
	})
	if err != nil {
		t.Fatalf("NativeSubscribe logs failed to open stream: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}

	recvErr := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		recvErr <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-recvErr:
			if err != nil {
				t.Fatalf("NativeSubscribe stream failed before upstream eth_subscribe: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
			}
		default:
		}
		if hardhatSawLogsSubscribe(t, ctx, upstream, filterAddress) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("hardhat did not receive eth_subscribe logs request\nlogs:\n%s", nodecore.Logs(ctx))
}

func latestBlockSelector() *dshackle.Selector {
	return &dshackle.Selector{
		SelectorType: &dshackle.Selector_HeightSelector{
			HeightSelector: &dshackle.HeightSelector{
				HeightOrNumber: &dshackle.HeightSelector_Tag{Tag: dshackle.BlockTag_LATEST},
			},
		},
	}
}

func grpcNativeSubscribeNodecoreConfig(upstream *harness.RPCNode) string {
	wsURL := strings.Replace(upstream.InternalURL(), "http://", "ws://", 1)
	return fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
upstream-config:
  mode: strict
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
    - id: %s
      chain: ethereum
      head-connector: websocket
      poll-interval: 500ms
      connectors:
        - type: json-rpc
          url: %q
        - type: websocket
          url: %q
      options:
		disable-liveness-subscription-validation: true
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL(), wsURL)
}

func assertSubscriptionAdvertised(t *testing.T, status *dshackle.SubscribeChainStatusResponse, want string) {
	t.Helper()
	for _, event := range status.GetChainDescription().GetChainEvent() {
		for _, sub := range event.GetSupportedSubscriptionsEvent().GetSubs() {
			if sub == want {
				return
			}
		}
	}
	t.Fatalf("subscription %q is not advertised: %+v", want, status)
}

func hardhatSawLogsSubscribe(t *testing.T, ctx context.Context, upstream *harness.RPCNode, address string) bool {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, upstream.Endpoint, "hardhat_getE2ERequests", []any{})
	requests, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("unexpected hardhat request log response: %+v", resp)
	}
	for _, raw := range requests {
		req, ok := raw.(map[string]any)
		if !ok || req["method"] != "eth_subscribe" {
			continue
		}
		params, ok := req["params"].([]any)
		if !ok || len(params) != 2 || params[0] != "logs" {
			continue
		}
		filter, ok := params[1].(map[string]any)
		if ok && filter["address"] == address {
			return true
		}
	}
	return false
}
