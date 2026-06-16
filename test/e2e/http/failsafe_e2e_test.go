//go:build e2e

package http_e2e

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPRetryFallbackAcrossUpstreamsBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-failsafe-primary", SeedTxs: 1, Rules: `[{
			"method":"eth_getTransactionCount",
			"error":{"code":-32000,"message":"request timed out"}
		}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-failsafe-fallback", SeedTxs: 7},
	)
	primary, fallback := nodes[0], nodes[1]
	defer primary.Terminate(ctx)
	defer fallback.Terminate(ctx)

	scorePath := "/tmp/nodecore-e2e-failsafe-score.js"
	nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
		Order:           "primary, fallback",
		PassOnError:     true,
		IncludeDefault:  false,
		RetryAttempts:   2,
		ScorePolicyPath: scorePath,
		Labels: map[string]string{
			primary.Alias:  "primary",
			fallback.Alias: "fallback",
		},
	}, primary, fallback), fixedOrderScorePolicyFiles(scorePath, primary, fallback))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, primary)
	harness.ClearHardhatRequests(t, ctx, fallback)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != fallback.NonceHex {
		t.Fatalf("retry fallback returned wrong upstream result: got %s want fallback %s; response=%+v\nlogs:\n%s", got, fallback.NonceHex, resp, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, primary, "eth_getTransactionCount"); got == 0 {
		t.Fatalf("retry fallback did not exercise primary upstream\nlogs:\n%s", nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, fallback, "eth_getTransactionCount"); got == 0 {
		t.Fatalf("retry fallback did not exercise fallback upstream\nlogs:\n%s", nodecore.Logs(ctx))
	}
}

func TestHTTPRetryDoesNotFallbackOnNonRetryableErrorBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-failsafe-nonretryable", SeedTxs: 1, Rules: `[{
			"method":"eth_getTransactionCount",
			"error":{"code":3,"message":"execution reverted: e2e non-retryable"}
		}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-failsafe-unused-fallback", SeedTxs: 7},
	)
	primary, fallback := nodes[0], nodes[1]
	defer primary.Terminate(ctx)
	defer fallback.Terminate(ctx)

	scorePath := "/tmp/nodecore-e2e-failsafe-nonretryable-score.js"
	nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
		Order:           "primary, fallback",
		PassOnError:     true,
		IncludeDefault:  false,
		RetryAttempts:   2,
		ScorePolicyPath: scorePath,
		Labels: map[string]string{
			primary.Alias:  "primary",
			fallback.Alias: "fallback",
		},
	}, primary, fallback), fixedOrderScorePolicyFiles(scorePath, primary, fallback))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, primary)
	harness.ClearHardhatRequests(t, ctx, fallback)

	resp := callUntilError(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if resp["error"] == nil {
		t.Fatalf("non-retryable primary error should be returned to client: response=%+v\nlogs:\n%s", resp, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, primary, "eth_getTransactionCount"); got == 0 {
		t.Fatalf("non-retryable test did not exercise primary upstream\nlogs:\n%s", nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, fallback, "eth_getTransactionCount"); got != 0 {
		t.Fatalf("non-retryable error should not fall back: fallback calls=%d want 0\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}
