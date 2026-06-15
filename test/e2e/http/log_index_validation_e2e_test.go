//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const logIndexTxA = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const logIndexTxB = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestHTTPLogIndexValidationExcludesInvalidUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validation-logindex-bad", SeedTxs: 9, Rules: logIndexRules(false)},
		harness.HardhatNodeSpec{Alias: "hardhat-validation-logindex-good", SeedTxs: 3, Rules: logIndexRules(true)},
	)
	invalid, valid := nodes[0], nodes[1]
	defer invalid.Terminate(ctx)
	defer valid.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, logIndexValidationConfig(invalid, valid))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != valid.NonceHex {
		t.Fatalf("log-index validation did not exclude invalid upstream: got %s want %s; response=%+v\nlogs:\n%s", got, valid.NonceHex, resp, nodecore.Logs(ctx))
	}
}

func logIndexRules(valid bool) string {
	secondFirst := "0x0"
	if valid {
		secondFirst = "0x2"
	}
	return fmt.Sprintf(`[
		{"method":"eth_blockNumber","result":"0x10"},
		{"method":"eth_getBlockByNumber","params":["0x10",true],"result":{"transactions":[{"hash":%q},{"hash":%q}]}},
		{"method":"eth_getTransactionReceipt","params":[%q],"result":{"logs":[{"logIndex":"0x0"},{"logIndex":"0x1"}]}},
		{"method":"eth_getTransactionReceipt","params":[%q],"result":{"logs":[{"logIndex":%q}]}}
	]`, logIndexTxA, logIndexTxB, logIndexTxA, logIndexTxB, secondFirst)
}

func logIndexValidationConfig(invalid, valid *harness.RPCNode) string {
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
	for _, upstream := range []*harness.RPCNode{invalid, valid} {
		label := "fallback"
		if upstream == invalid {
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
        disable-log-index-validation: false
        validate-client-version: false
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, label, upstream.InternalURL())
	}
	return out
}
