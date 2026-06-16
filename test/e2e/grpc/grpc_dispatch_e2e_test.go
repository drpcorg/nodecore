//go:build e2e

package grpc_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const hardhatAccount = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"

func TestGrpcNativeCallDispatchMaximumValueBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-grpc-max-a", SeedTxs: 1},
		harness.HardhatNodeSpec{Alias: "hardhat-grpc-max-b", SeedTxs: 6},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, grpcDispatchNodecoreConfig(nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()

	chain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: chain, ID: 101, Method: "eth_getTransactionCount", Payload: fmt.Sprintf(`["%s","latest"]`, hardhatAccount)}, 60*time.Second)
	got := decodeJSONRPCStringResult(t, item.GetPayload())
	if got != nodeB.NonceHex {
		t.Fatalf("gRPC NativeCall maximum-value mismatch: got %s want %s item=%+v\nlogs:\n%s", got, nodeB.NonceHex, item, nodecore.Logs(ctx))
	}
}

func grpcDispatchNodecoreConfig(upstreams ...*harness.RPCNode) string {
	out := `server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
upstream-config:
  mode: default
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: true
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`
	for _, upstream := range upstreams {
		out += fmt.Sprintf(`    - id: %s
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
`, upstream.Alias, upstream.InternalURL())
	}
	return out
}
