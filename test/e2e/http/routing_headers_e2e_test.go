//go:build e2e

package http_e2e

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const (
	groupLabelHeader = "X-Nodecore-Group-Label"
	upstreamIdHeader = "X-Nodecore-Upstream-Id"
)

// routingNodes is the upstream set every routing-headers test runs against,
// told apart by their nonce. Under the label-balancing config of
// startRoutingNodecore only paid is routable: archive is not in the order,
// plain carries no label and the default group is off.
type routingNodes struct {
	paid  *harness.HardhatNode
	archA *harness.HardhatNode // answers eth_getTransactionCount with a retryable error
	archB *harness.HardhatNode
	plain *harness.HardhatNode
}

func startRoutingNodes(t *testing.T, ctx context.Context, networkName, prefix string) routingNodes {
	t.Helper()
	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: prefix + "-paid", SeedTxs: 1},
		harness.HardhatNodeSpec{Alias: prefix + "-arch-a", SeedTxs: 2, Rules: retryableTransactionCountErrorRule()},
		harness.HardhatNodeSpec{Alias: prefix + "-arch-b", SeedTxs: 3},
		harness.HardhatNodeSpec{Alias: prefix + "-plain", SeedTxs: 4},
	)
	t.Cleanup(func() {
		for _, node := range nodes {
			node.Terminate(ctx)
		}
	})
	return routingNodes{paid: nodes[0], archA: nodes[1], archB: nodes[2], plain: nodes[3]}
}

func startRoutingNodecore(t *testing.T, ctx context.Context, networkName string, nodes routingNodes, routingHeaders bool, keyGroupLabel string) *harness.Nodecore {
	t.Helper()
	scorePath := "/tmp/nodecore-e2e-routing-headers-score.js"
	nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, labelBalancingAdvancedConfig(labelBalancingOptions{
		Order:           "paid",
		PassOnError:     true,
		IncludeDefault:  false,
		RetryAttempts:   3,
		ScorePolicyPath: scorePath,
		RoutingHeaders:  routingHeaders,
		KeyGroupLabel:   keyGroupLabel,
		Labels: map[string]string{
			nodes.paid.Alias:  "paid",
			nodes.archA.Alias: "archive",
			nodes.archB.Alias: "archive",
		},
	}, nodes.paid, nodes.archA, nodes.archB, nodes.plain), fixedOrderScorePolicyFiles(scorePath, nodes.paid, nodes.archA, nodes.archB, nodes.plain))
	t.Cleanup(func() { nodecore.Terminate(ctx) })
	return nodecore
}

var transactionCountParams = []any{hardhatAccount, "latest"}

// routedNonce polls eth_getTransactionCount with the headers until it
// succeeds and returns the nonce, which names the upstream that answered.
func routedNonce(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, headers map[string]string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.JSONRPCWithHeaders(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", transactionCountParams, headers)
		if _, hasErr := last["error"]; !hasErr {
			return harness.ResultString(t, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("eth_getTransactionCount with headers %v did not succeed before timeout; last=%+v\nlogs:\n%s", headers, last, nodecore.Logs(ctx))
	return ""
}

func assertRoutedTo(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, headers map[string]string, expected *harness.HardhatNode) {
	t.Helper()
	if got := routedNonce(t, ctx, nodecore, headers); got != expected.NonceHex {
		t.Fatalf("headers %v: answered with nonce %s, want %s (%s)\nlogs:\n%s", headers, got, expected.NonceHex, expected.Alias, nodecore.Logs(ctx))
	}
}

func TestHTTPRoutingHeadersBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()
	nodes := startRoutingNodes(t, ctx, networkName, "hardhat-routing")
	nodecore := startRoutingNodecore(t, ctx, networkName, nodes, true, "")

	// Every error subtest below runs after this success, so an upstream that is
	// simply not ready yet is never mistaken for the expected error.
	t.Run("no_header_follows_label_balancing", func(t *testing.T) {
		assertRoutedTo(t, ctx, nodecore, nil, nodes.paid)
	})

	// arch-a fails first: the retry must stay in the group although
	// pass-on-error is on and "archive" is not in the order at all
	t.Run("group_label_retries_inside_the_group", func(t *testing.T) {
		assertRoutedTo(t, ctx, nodecore, map[string]string{groupLabelHeader: "archive"}, nodes.archB)
	})

	t.Run("upstream_id_reaches_an_upstream_outside_order", func(t *testing.T) {
		assertRoutedTo(t, ctx, nodecore, map[string]string{upstreamIdHeader: nodes.plain.Alias}, nodes.plain)
	})

	t.Run("several_upstream_ids", func(t *testing.T) {
		headers := map[string]string{upstreamIdHeader: nodes.paid.Alias + ", " + nodes.plain.Alias}
		got := routedNonce(t, ctx, nodecore, headers)
		if got != nodes.paid.NonceHex && got != nodes.plain.NonceHex {
			t.Fatalf("answered with nonce %s, want one of the two named upstreams\nlogs:\n%s", got, nodecore.Logs(ctx))
		}
	})

	t.Run("both_headers_agree", func(t *testing.T) {
		assertRoutedTo(t, ctx, nodecore, map[string]string{groupLabelHeader: "archive", upstreamIdHeader: nodes.archB.Alias}, nodes.archB)
	})

	t.Run("both_headers_disagree", func(t *testing.T) {
		headers := map[string]string{groupLabelHeader: "archive", upstreamIdHeader: nodes.plain.Alias}
		harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", transactionCountParams, headers)
	})

	t.Run("unknown_upstream_id", func(t *testing.T) {
		headers := map[string]string{upstreamIdHeader: "no-such-upstream"}
		harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", transactionCountParams, headers)
	})

	// the one named upstream fails: its error is the answer, nobody steps in
	t.Run("single_failing_upstream_is_not_replaced", func(t *testing.T) {
		others := []*harness.HardhatNode{nodes.paid, nodes.archB, nodes.plain}
		before := make([]int, len(others))
		for i, node := range others {
			before[i] = harness.CountHardhatRequests(t, ctx, node, "eth_getTransactionCount")
		}

		headers := map[string]string{upstreamIdHeader: nodes.archA.Alias}
		harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", transactionCountParams, headers)

		for i, node := range others {
			if after := harness.CountHardhatRequests(t, ctx, node, "eth_getTransactionCount"); after != before[i] {
				t.Fatalf("%s served a request pinned to %s: eth_getTransactionCount count %d -> %d\nlogs:\n%s", node.Alias, nodes.archA.Alias, before[i], after, nodecore.Logs(ctx))
			}
		}
	})

	t.Run("batch_applies_to_every_item", func(t *testing.T) {
		batch := []map[string]any{
			{"jsonrpc": "2.0", "id": 1, "method": "eth_getTransactionCount", "params": transactionCountParams},
			{"jsonrpc": "2.0", "id": 2, "method": "eth_getTransactionCount", "params": transactionCountParams},
		}
		responses := harness.JSONRPCBatch(t, ctx, nodecore.HTTPURL, "ethereum", batch, map[string]string{upstreamIdHeader: nodes.plain.Alias})
		if len(responses) != len(batch) {
			t.Fatalf("batch response count mismatch: got %d want %d responses=%+v\nlogs:\n%s", len(responses), len(batch), responses, nodecore.Logs(ctx))
		}
		for _, response := range responses {
			if got := harness.ResultString(t, response); got != nodes.plain.NonceHex {
				t.Fatalf("batch item answered with nonce %s, want %s; responses=%+v\nlogs:\n%s", got, nodes.plain.NonceHex, responses, nodecore.Logs(ctx))
			}
		}
	})
}

// without server.routing-headers the header restricts nothing
func TestHTTPRoutingHeadersOffByDefaultBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()
	nodes := startRoutingNodes(t, ctx, networkName, "hardhat-routing-off")
	nodecore := startRoutingNodecore(t, ctx, networkName, nodes, false, "")

	assertRoutedTo(t, ctx, nodecore, map[string]string{upstreamIdHeader: nodes.plain.Alias}, nodes.paid)
}

// a routing header narrows within the key's upstreams and never leaves them
func TestHTTPRoutingHeadersWithKeyRestrictionBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()
	nodes := startRoutingNodes(t, ctx, networkName, "hardhat-routing-key")
	nodecore := startRoutingNodecore(t, ctx, networkName, nodes, true, "archive")

	inside := map[string]string{"X-Nodecore-Key": httpAuthKey, upstreamIdHeader: nodes.archB.Alias}
	assertRoutedTo(t, ctx, nodecore, inside, nodes.archB)

	outside := map[string]string{"X-Nodecore-Key": httpAuthKey, upstreamIdHeader: nodes.plain.Alias}
	harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", transactionCountParams, outside)
}
