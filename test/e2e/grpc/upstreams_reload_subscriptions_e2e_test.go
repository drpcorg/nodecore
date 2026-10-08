//go:build e2e

package grpc_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
	"github.com/drpcorg/public/pkg/dshackle"
	"github.com/gorilla/websocket"
)

// Client subscriptions that are served by the websocket of an upstream must be
// ended explicitly when the upstream is removed: a NativeSubscribe stream gets
// an error, a websocket client gets an error for its subscription. Silence is
// the one thing they must not get - a client can't tell it from a quiet chain.
func TestGrpcUpstreamsReloadEndsSubscriptionsOfRemovedUpstream(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	ethNode := harness.StartMockNode(t, ctx, networkName, harness.MockNodeSpec{Alias: "mock-subs-eth", ChainId: 1})
	defer ethNode.Terminate(ctx)
	polygonNode := harness.StartMockNode(t, ctx, networkName, harness.MockNodeSpec{Alias: "mock-subs-polygon", ChainId: 137, BlockTime: 200 * time.Millisecond})
	defer polygonNode.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, reloadSubscriptionsConfig(ethNode, polygonNode))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	connectionLosses := watchConnection(ctx, conn)

	ethChain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	polygonChain := dshackle.ChainRef(chains.GetChain("polygon").GrpcId)
	status := harness.WaitForFullChainStatus(t, ctx, client, nodecore, polygonChain, 45*time.Second)
	assertSubscriptionAdvertised(t, status, "newHeads")

	// a gRPC client subscription, served by the node behind the polygon upstream
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := client.NativeSubscribe(streamCtx, &dshackle.NativeSubscribeRequest{Chain: polygonChain, Method: "newHeads"})
	if err != nil {
		t.Fatalf("NativeSubscribe failed to open: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}
	grpcEvents := make(chan error, 100)
	go func() {
		for {
			_, err := stream.Recv()
			grpcEvents <- err
			if err != nil {
				return
			}
		}
	}()
	waitForSubscriptionEvent(t, nodecore, "the gRPC subscription", grpcEvents)

	// a websocket client subscription on the same upstream
	wsURL := strings.Replace(nodecore.HTTPURL, "http://", "ws://", 1) + "/queries/polygon"
	wsClient, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial nodecore websocket: %v\nlogs:\n%s", err, nodecore.Logs(ctx))
	}
	defer wsClient.Close()
	if err := wsClient.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)); err != nil {
		t.Fatalf("websocket subscribe: %v", err)
	}
	wsEvents := make(chan error, 100)
	go func() {
		for {
			_, message, err := wsClient.ReadMessage()
			if err == nil {
				var frame struct {
					Error  json.RawMessage `json:"error"`
					Params struct {
						Error json.RawMessage `json:"error"`
					} `json:"params"`
				}
				_ = json.Unmarshal(message, &frame)
				if len(frame.Error) > 0 || len(frame.Params.Error) > 0 {
					err = fmt.Errorf("error frame: %s", message)
				}
			}
			wsEvents <- err
			if err != nil {
				return
			}
		}
	}()
	waitForSubscriptionEvent(t, nodecore, "the websocket subscription confirmation", wsEvents)
	waitForSubscriptionEvent(t, nodecore, "the websocket subscription", wsEvents)

	// the polygon upstream goes away
	removed := time.Now()
	nodecore.WriteConfig(t, ctx, reloadSubscriptionsConfig(ethNode))

	grpcEnd := waitForSubscriptionEnd(t, nodecore, "the gRPC subscription", grpcEvents)
	t.Logf("the gRPC subscription ended %s after the config change: %v", time.Since(removed).Round(10*time.Millisecond), grpcEnd)
	wsEnd := waitForSubscriptionEnd(t, nodecore, "the websocket subscription", wsEvents)
	t.Logf("the websocket subscription ended %s after the config change: %v", time.Since(removed).Round(10*time.Millisecond), wsEnd)

	// nothing else noticed
	harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: ethChain, ID: 1, Method: "eth_gasPrice", Payload: `[]`}, 10*time.Second)
	if losses := connectionLosses.Load(); losses != 0 {
		t.Fatalf("the gRPC connection left READY %d times\nlogs:\n%s", losses, nodecore.Logs(ctx))
	}
}

func waitForSubscriptionEvent(t *testing.T, nodecore *harness.Nodecore, what string, events <-chan error) {
	t.Helper()
	select {
	case err := <-events:
		if err != nil {
			t.Fatalf("%s failed before the upstream was removed: %v\nlogs:\n%s", what, err, nodecore.Logs(context.Background()))
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("%s delivers nothing\nlogs:\n%s", what, nodecore.Logs(context.Background()))
	}
}

// waitForSubscriptionEnd drains the events that were still on their way and
// returns the error the subscription ended with.
func waitForSubscriptionEnd(t *testing.T, nodecore *harness.Nodecore, what string, events <-chan error) error {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case err := <-events:
			if err != nil {
				return err
			}
		case <-timeout:
			t.Fatalf("%s was left open after its upstream was removed\nlogs:\n%s", what, nodecore.Logs(context.Background()))
		}
	}
}

func reloadSubscriptionsConfig(ethNode *harness.RPCNode, wsNodes ...*harness.RPCNode) string {
	out := fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
upstream-config:
  mode: strict
  reload:
    watch-interval: 200ms
  chain-defaults:
    polygon:
      local-subscriptions:
        enable: false
  upstreams:
    - id: eth-upstream
      chain: ethereum
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
`, ethNode.InternalURL())
	for _, node := range wsNodes {
		out += fmt.Sprintf(`    - id: polygon-ws-upstream
      chain: polygon
      head-connector: websocket
      poll-interval: 500ms
      connectors:
        - type: json-rpc
          url: %q
        - type: websocket
          url: %q
      options:
        internal-timeout: 5s
        disable-liveness-subscription-validation: true
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, node.InternalURL(), node.InternalWsURL())
	}
	return out
}
