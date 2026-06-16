//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPClientVersionValidationExcludesIncompatibleUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validation-client-bad", SeedTxs: 9, Rules: `[
			{"method":"web3_clientVersion","result":"erigon/v2.40.0/linux-amd64/go1.20"}
		]`},
		harness.HardhatNodeSpec{Alias: "hardhat-validation-client-good", SeedTxs: 3},
	)
	incompatible, compatible := nodes[0], nodes[1]
	defer incompatible.Terminate(ctx)
	defer compatible.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, clientVersionValidationConfig(incompatible, compatible))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != compatible.NonceHex {
		t.Fatalf("client-version validation did not exclude incompatible upstream: got %s want %s; response=%+v\nlogs:\n%s", got, compatible.NonceHex, resp, nodecore.Logs(ctx))
	}
}

func clientVersionValidationConfig(incompatible, compatible *harness.RPCNode) string {
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
    order: [primary, fallback]
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
	for _, upstream := range []*harness.RPCNode{incompatible, compatible} {
		label := "fallback"
		if upstream == incompatible {
			label = "primary"
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
        validate-client-version: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, label, upstream.InternalURL())
	}
	return out
}
