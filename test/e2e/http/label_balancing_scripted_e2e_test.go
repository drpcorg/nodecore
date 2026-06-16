//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPLabelBalancingFallbackAndDefaultBlackbox(t *testing.T) {
	t.Run("paid_unavailable_falls_back_to_free", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-label-fallback-paid", SeedTxs: 6},
			harness.HardhatNodeSpec{Alias: "hardhat-label-fallback-free", SeedTxs: 2},
		)
		paid, free := nodes[0], nodes[1]
		defer paid.Terminate(ctx)
		defer free.Terminate(ctx)

		nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
			Order:          "paid, free",
			IncludeDefault: false,
			Labels:         map[string]string{paid.Alias: "paid", free.Alias: "free"},
			DisabledMethods: map[string][]string{
				paid.Alias: {"eth_getTransactionCount"},
			},
		}, paid, free))
		defer nodecore.Terminate(ctx)

		resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
		if got := harness.ResultString(t, resp); got != free.NonceHex {
			t.Fatalf("label balancing did not fall back to free group: got %s want %s; response=%+v\nlogs:\n%s", got, free.NonceHex, resp, nodecore.Logs(ctx))
		}
	})

	t.Run("default_group_is_used_when_included", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-label-default-paid", SeedTxs: 6},
			harness.HardhatNodeSpec{Alias: "hardhat-label-default-free", SeedTxs: 2},
			harness.HardhatNodeSpec{Alias: "hardhat-label-default-node", SeedTxs: 4},
		)
		paid, free, def := nodes[0], nodes[1], nodes[2]
		defer paid.Terminate(ctx)
		defer free.Terminate(ctx)
		defer def.Terminate(ctx)

		nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
			Order:          "paid, free",
			IncludeDefault: true,
			Labels:         map[string]string{paid.Alias: "paid", free.Alias: "free"},
			DisabledMethods: map[string][]string{
				paid.Alias: {"eth_getTransactionCount"},
				free.Alias: {"eth_getTransactionCount"},
			},
		}, paid, free, def))
		defer nodecore.Terminate(ctx)

		resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
		if got := harness.ResultString(t, resp); got != def.NonceHex {
			t.Fatalf("label balancing did not use included default group: got %s want %s; response=%+v\nlogs:\n%s", got, def.NonceHex, resp, nodecore.Logs(ctx))
		}
	})

	t.Run("default_group_is_excluded_when_disabled", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		def := harness.StartHardhatNode(t, ctx, networkName, "hardhat-label-default-excluded", 4)
		defer def.Terminate(ctx)

		nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
			Order:          "paid",
			IncludeDefault: false,
		}, def))
		defer nodecore.Terminate(ctx)

		callUntilError(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	})
}

func TestHTTPLabelBalancingPassOnErrorBlackbox(t *testing.T) {
	t.Run("false_stays_in_current_group", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-false-paid-a", SeedTxs: 1, Rules: retryableTransactionCountErrorRule()},
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-false-paid-b", SeedTxs: 6},
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-false-free", SeedTxs: 2},
		)
		paidA, paidB, free := nodes[0], nodes[1], nodes[2]
		defer paidA.Terminate(ctx)
		defer paidB.Terminate(ctx)
		defer free.Terminate(ctx)

		scorePath := "/tmp/nodecore-e2e-pass-on-error-score.js"
		nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
			Order:           "paid, free",
			PassOnError:     false,
			IncludeDefault:  false,
			RetryAttempts:   2,
			ScorePolicyPath: scorePath,
			Labels: map[string]string{
				paidA.Alias: "paid",
				paidB.Alias: "paid",
				free.Alias:  "free",
			},
		}, paidA, paidB, free), fixedOrderScorePolicyFiles(scorePath, paidA, paidB, free))
		defer nodecore.Terminate(ctx)

		resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
		if got := harness.ResultString(t, resp); got != paidB.NonceHex {
			t.Fatalf("pass-on-error=false did not stay in paid group after retryable error: got %s want %s; response=%+v\nlogs:\n%s", got, paidB.NonceHex, resp, nodecore.Logs(ctx))
		}
	})

	t.Run("true_jumps_to_next_group", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-true-paid-a", SeedTxs: 1, Rules: retryableTransactionCountErrorRule()},
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-true-paid-b", SeedTxs: 6},
			harness.HardhatNodeSpec{Alias: "hardhat-pass-error-true-free", SeedTxs: 2},
		)
		paidA, paidB, free := nodes[0], nodes[1], nodes[2]
		defer paidA.Terminate(ctx)
		defer paidB.Terminate(ctx)
		defer free.Terminate(ctx)

		scorePath := "/tmp/nodecore-e2e-pass-on-error-score.js"
		nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
			Order:           "paid, free",
			PassOnError:     true,
			IncludeDefault:  false,
			RetryAttempts:   2,
			ScorePolicyPath: scorePath,
			Labels: map[string]string{
				paidA.Alias: "paid",
				paidB.Alias: "paid",
				free.Alias:  "free",
			},
		}, paidA, paidB, free), fixedOrderScorePolicyFiles(scorePath, paidA, paidB, free))
		defer nodecore.Terminate(ctx)

		resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
		if got := harness.ResultString(t, resp); got != free.NonceHex {
			t.Fatalf("pass-on-error=true did not jump to free group after retryable error: got %s want %s; response=%+v\nlogs:\n%s", got, free.NonceHex, resp, nodecore.Logs(ctx))
		}
	})
}

func retryableTransactionCountErrorRule() string {
	return `[{"method":"eth_getTransactionCount","error":{"code":-32000,"message":"request timed out"}}]`
}

func fixedOrderScorePolicyFiles(path string, upstreams ...*harness.RPCNode) map[string]string {
	order := ""
	for i, upstream := range upstreams {
		if i > 0 {
			order += ","
		}
		order += fmt.Sprintf("%q", upstream.Alias)
	}
	return map[string]string{
		path: fmt.Sprintf(`function sortUpstreams(upstreamData) {
  const preferred = [%s];
  const rank = new Map(preferred.map((id, idx) => [id, idx]));
  const sorted = upstreamData.map((u) => u.id).sort((a, b) => (rank.get(a) ?? 9999) - (rank.get(b) ?? 9999));
  return {
    sortedUpstreams: sorted,
    scores: upstreamData.map((u) => ({ id: u.id, score: 1000 - (rank.get(u.id) ?? 9999) }))
  };
}`, order),
	}
}

func scorePolicyPathConfig(path string) string {
	if path == "" {
		return ""
	}
	return fmt.Sprintf("    calculation-function-file-path: %q\n", path)
}

type labelBalancingOptions struct {
	Order           string
	PassOnError     bool
	IncludeDefault  bool
	RetryAttempts   int
	Labels          map[string]string
	DisabledMethods map[string][]string
	ScorePolicyPath string
}

func labelBalancingAdvancedConfig(opts labelBalancingOptions, upstreams ...*harness.RPCNode) string {
	retryAttempts := opts.RetryAttempts
	if retryAttempts == 0 {
		retryAttempts = 1
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
  failsafe-config:
    retry:
      attempts: %d
      delay: 50ms
      max-delay: 50ms
      jitter: 1ms
  label-balancing:
    order: [%s]
    pass-on-error: %t
    include-default: %t
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 100ms
%s  upstreams:
`, retryAttempts, opts.Order, opts.PassOnError, opts.IncludeDefault, scorePolicyPathConfig(opts.ScorePolicyPath))
	for _, upstream := range upstreams {
		out += fmt.Sprintf(`    - id: %s
      chain: ethereum
`, upstream.Alias)
		if label := opts.Labels[upstream.Alias]; label != "" {
			out += fmt.Sprintf("      group-labels: [%s]\n", label)
		}
		out += fmt.Sprintf(`      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
`, upstream.InternalURL())
		if disabled := opts.DisabledMethods[upstream.Alias]; len(disabled) > 0 {
			out += "      methods:\n        disable:\n"
			for _, method := range disabled {
				out += fmt.Sprintf("          - %q\n", method)
			}
		}
		out += `      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`
	}
	return out
}
