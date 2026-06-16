//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPDispatchMaximumValueWaitsForSlowMaximumBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-max-fast-low", Rules: `[{
			"method":"eth_getTransactionCount",
			"result":"0x1"
		}]`},
		harness.HardhatNodeSpec{Alias: "hardhat-max-slow-high", Rules: `[{
			"method":"eth_getTransactionCount",
			"delayMs":1200,
			"result":"0x9"
		}]`},
	)
	fastLow, slowHigh := nodes[0], nodes[1]
	defer fastLow.Terminate(ctx)
	defer slowHigh.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{MaximumValue: true}, fastLow, slowHigh))
	defer nodecore.Terminate(ctx)

	started := time.Now()
	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != "0x9" {
		t.Fatalf("maximum-value slow maximum mismatch: got %s want 0x9; response=%+v\nlogs:\n%s", got, resp, nodecore.Logs(ctx))
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("maximum-value returned before slow upstream could answer: elapsed=%s response=%+v", elapsed, resp)
	}
}

func TestHTTPDispatchBroadcastErrorToleranceBlackbox(t *testing.T) {
	t.Run("partial_failure_still_reaches_successful_node", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-broadcast-ok"},
			harness.HardhatNodeSpec{Alias: "hardhat-broadcast-fail", Rules: `[{
				"method":"eth_sendRawTransaction",
				"error":{"code":-32000,"message":"scripted broadcast failure"}
			}]`},
		)
		okNode, failNode := nodes[0], nodes[1]
		defer okNode.Terminate(ctx)
		defer failNode.Terminate(ctx)

		fundBroadcastSender(t, ctx, okNode)
		fundBroadcastSender(t, ctx, failNode)
		rawTx, wantHash := signedHardhatTx(t, hardhatNonce(t, ctx, okNode, broadcastSender))

		nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{Broadcast: true}, okNode, failNode))
		defer nodecore.Terminate(ctx)

		_ = harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_sendRawTransaction", []any{rawTx})
		waitTxOnHardhat(t, ctx, okNode, wantHash)
	})

	t.Run("all_failures_return_public_error", func(t *testing.T) {
		ctx := context.Background()
		networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
		defer cleanupNetwork()

		nodes := harness.StartHardhatNodes(t, ctx, networkName,
			harness.HardhatNodeSpec{Alias: "hardhat-broadcast-fail-a", Rules: `[{"method":"eth_sendRawTransaction","error":{"code":-32000,"message":"scripted broadcast failure a"}}]`},
			harness.HardhatNodeSpec{Alias: "hardhat-broadcast-fail-b", Rules: `[{"method":"eth_sendRawTransaction","error":{"code":-32000,"message":"scripted broadcast failure b"}}]`},
		)
		nodeA, nodeB := nodes[0], nodes[1]
		defer nodeA.Terminate(ctx)
		defer nodeB.Terminate(ctx)

		fundBroadcastSender(t, ctx, nodeA)
		fundBroadcastSender(t, ctx, nodeB)
		rawTx, _ := signedHardhatTx(t, hardhatNonce(t, ctx, nodeA, broadcastSender))

		nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{Broadcast: true}, nodeA, nodeB))
		defer nodecore.Terminate(ctx)

		callUntilError(t, ctx, nodecore, "eth_sendRawTransaction", []any{rawTx})
	})
}

func callUntilError(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, method string, params any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", method, params)
		if _, hasErr := last["error"]; hasErr {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s did not return an error before timeout; last=%+v\nlogs:\n%s", method, last, nodecore.Logs(ctx))
	return nil
}

func toString(value any) string { return fmt.Sprint(value) }
