//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPGasPriceValidationExcludesInvalidUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validation-gas-bad", SeedTxs: 9, Rules: `[
			{"method":"eth_chainId","result":"0x38"},
			{"method":"net_version","result":"56"},
			{"method":"eth_gasPrice","result":"0xb2d05e00"}
		]`},
		harness.HardhatNodeSpec{Alias: "hardhat-validation-gas-good", SeedTxs: 3, Rules: `[
			{"method":"eth_chainId","result":"0x38"},
			{"method":"net_version","result":"56"},
			{"method":"eth_gasPrice","result":"0x1"}
		]`},
	)
	invalid, valid := nodes[0], nodes[1]
	defer invalid.Terminate(ctx)
	defer valid.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, gasPriceValidationConfig(invalid, valid))
	defer nodecore.Terminate(ctx)

	resp := callUntilResultForChain(t, ctx, nodecore, "bsc", "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != valid.NonceHex {
		t.Fatalf("gas-price validation did not exclude invalid upstream: got %s want %s; response=%+v\nlogs:\n%s", got, valid.NonceHex, resp, nodecore.Logs(ctx))
	}
}

func gasPriceValidationConfig(invalid, valid *harness.RPCNode) string {
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
    bsc:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`
	for _, upstream := range []*harness.RPCNode{invalid, valid} {
		label := "fallback"
		if upstream == invalid {
			label = "primary"
		}
		out += fmt.Sprintf(`    - id: %s
      chain: bsc
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
        validate-client-version: false
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, label, upstream.InternalURL())
	}
	return out
}

func callUntilResultForChain(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, chain, method string, params any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.JSONRPC(t, ctx, nodecore.HTTPURL, chain, method, params)
		if _, hasErr := last["error"]; !hasErr {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s/%s did not succeed before timeout; last=%+v\nlogs:\n%s", chain, method, last, nodecore.Logs(ctx))
	return nil
}
