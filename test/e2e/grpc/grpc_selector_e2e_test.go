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

func TestGrpcNativeCallLabelBalancingSelectorBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-grpc-label-paid", SeedTxs: 5},
		harness.HardhatNodeSpec{Alias: "hardhat-grpc-label-free", SeedTxs: 2},
	)
	paid, free := nodes[0], nodes[1]
	defer paid.Terminate(ctx)
	defer free.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, grpcLabelBalancingConfig(paid, free))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	chain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: chain, ID: 101, Method: "eth_getTransactionCount", Payload: fmt.Sprintf(`["%s","latest"]`, hardhatAccount)}, 60*time.Second)
	got := decodeJSONRPCStringResult(t, item.GetPayload())
	if got != paid.NonceHex {
		t.Fatalf("gRPC NativeCall label selector mismatch: got %s want paid %s item=%+v\nlogs:\n%s", got, paid.NonceHex, item, nodecore.Logs(ctx))
	}
}

func grpcLabelBalancingConfig(upstreams ...*harness.RPCNode) string {
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
  label-balancing:
    order: [paid]
    include-default: false
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`
	for i, upstream := range upstreams {
		label := "free"
		if i == 0 {
			label = "paid"
		}
		out += fmt.Sprintf(`    - id: %s
      chain: ethereum
      group-labels: [%s]
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
`, upstream.Alias, label, upstream.InternalURL())
	}
	return out
}
