//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPValidationExcludesSyncingUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validator-syncing", SeedTxs: 9, Rules: `[{
			"method":"eth_syncing",
			"result":{"startingBlock":"0x1","currentBlock":"0x1","highestBlock":"0x100"}
		}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-validator-not-syncing", SeedTxs: 3, Rules: `[{"method":"eth_syncing","result":false}]`},
	)
	syncing, ready := nodes[0], nodes[1]
	defer syncing.Terminate(ctx)
	defer ready.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, validatorNodecoreConfig(syncing, ready, validatorOptions{ValidateSyncing: true}))
	defer nodecore.Terminate(ctx)

	got := waitForTransactionCountResult(t, ctx, nodecore, ready.NonceHex, 60*time.Second)
	if got != ready.NonceHex {
		t.Fatalf("syncing validator did not route to non-syncing fallback: got %s want %s\nlogs:\n%s", got, ready.NonceHex, nodecore.Logs(ctx))
	}
}

func TestHTTPValidationExcludesPeerlessUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validator-peerless", SeedTxs: 8, Rules: `[{"method":"net_peerCount","result":"0x0"}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-validator-peered", SeedTxs: 4, Rules: `[{"method":"net_peerCount","result":"0xa"}]`},
	)
	peerless, peered := nodes[0], nodes[1]
	defer peerless.Terminate(ctx)
	defer peered.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, validatorNodecoreConfig(peerless, peered, validatorOptions{ValidatePeers: true, MinPeers: 1}))
	defer nodecore.Terminate(ctx)

	got := waitForTransactionCountResult(t, ctx, nodecore, peered.NonceHex, 60*time.Second)
	if got != peered.NonceHex {
		t.Fatalf("peer validator did not route to peered fallback: got %s want %s\nlogs:\n%s", got, peered.NonceHex, nodecore.Logs(ctx))
	}
}

func TestHTTPValidationExcludesLowCallLimitUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-validator-low-call-limit", SeedTxs: 8, Rules: `[{
			"method":"eth_call",
			"error":{"code":-32000,"message":"rpc.returndata.limit exceeded in e2e"}
		}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-validator-ok-call-limit", SeedTxs: 4, Rules: `[{"method":"eth_call","result":"0x"}]`},
	)
	lowLimit, okLimit := nodes[0], nodes[1]
	defer lowLimit.Terminate(ctx)
	defer okLimit.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, validatorNodecoreConfig(lowLimit, okLimit, validatorOptions{ValidateCallLimit: true, CallLimitSize: 1024}))
	defer nodecore.Terminate(ctx)

	got := waitForTransactionCountResult(t, ctx, nodecore, okLimit.NonceHex, 60*time.Second)
	if got != okLimit.NonceHex {
		t.Fatalf("call-limit validator did not route to ok fallback: got %s want %s\nlogs:\n%s", got, okLimit.NonceHex, nodecore.Logs(ctx))
	}
}

type validatorOptions struct {
	ValidateSyncing   bool
	ValidatePeers     bool
	MinPeers          int
	ValidateCallLimit bool
	CallLimitSize     int
}

func validatorNodecoreConfig(primary, fallback *harness.RPCNode, opts validatorOptions) string {
	minPeers := opts.MinPeers
	if minPeers == 0 {
		minPeers = 1
	}
	callLimitSize := opts.CallLimitSize
	if callLimitSize == 0 {
		callLimitSize = 1000000
	}
	out := fmt.Sprintf(`server:
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
`)
	for _, upstream := range []*harness.RPCNode{primary, fallback} {
		label := "fallback"
		if upstream == primary {
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
        validation-interval: 1s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-client-version: false
        validate-syncing: %t
        validate-peers: %t
        min-peers: %d
        validate-call-limit: %t
        call-limit-size: %d
`, upstream.Alias, label, upstream.InternalURL(), opts.ValidateSyncing, opts.ValidatePeers, minPeers, opts.ValidateCallLimit, callLimitSize)
	}
	return out
}

func waitForTransactionCountResult(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", []any{hardhatAccount, "latest"})
		if _, hasErr := last["error"]; !hasErr {
			got := harness.ResultString(t, last)
			if got == want {
				return got
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("eth_getTransactionCount did not return %s before timeout; last=%+v\nlogs:\n%s", want, last, nodecore.Logs(ctx))
	return ""
}
