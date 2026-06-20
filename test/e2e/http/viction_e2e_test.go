//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestVictionStartupAndBasicRequest(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	// Use a hardhat node as the upstream — hardhat responds to
	// eth_getBlockByNumber("finalized",…) by returning the latest block,
	// but nodecore itself must NOT poll finalized because the Viction
	// chain config sets support-finalized-block-tag: false.
	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-viction", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, victionNodecoreConfig(upstream))
	defer nodecore.Terminate(ctx)

	// Verify health and readiness.
	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	waitReady(t, nodecore)

	// Send a basic JSON-RPC request (eth_blockNumber) through nodecore
	// to confirm the Viction chain routing works end-to-end.
	result := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "viction", "eth_blockNumber", []any{}))
	if result == "" {
		t.Fatal("expected hex block number, got empty result")
	}

	t.Logf("Viction nodecore healthy, eth_blockNumber = %s", result)
}

func TestVictionRejectsFinalizedTag(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-viction", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, victionNodecoreConfig(upstream))
	defer nodecore.Terminate(ctx)

	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	waitReady(t, nodecore)

	// Request eth_getBlockByNumber with "finalized" tag — on Viction this
	// must be rejected at flow level with -32602 without reaching the upstream.
	resp := harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "viction", "eth_getBlockByNumber", []any{"finalized", false}, nil)
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected Viction finalized tag error shape: %+v", resp)
	}
	code, _ := errObj["code"].(float64)
	message, _ := errObj["message"].(string)
	if int(code) != -32602 {
		t.Fatalf("expected -32602 for finalized tag on Viction, got code=%v message=%q response=%+v", errObj["code"], message, resp)
	}
	t.Logf("Viction correctly rejects finalized tag: %s", message)
}

func TestVictionNoFinalizedBlockPolling(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-viction", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, victionNodecoreConfig(upstream))
	defer nodecore.Terminate(ctx)

	// Wait for nodecore to become ready and let a couple of poll cycles pass.
	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	waitReady(t, nodecore)

	// Clear the hardhat request log so we only see requests after startup.
	harness.ClearHardhatRequests(t, ctx, upstream)

	// Wait long enough for at least 2 poll intervals (poll-interval is 1s).
	time.Sleep(3 * time.Second)

	// Verify that nodecore NEVER sent eth_getBlockByNumber with "finalized"
	// or "safe" params — Viction config disables both.
	hardhatReqCount := countHardhatRequestsByMethodAndParam(t, ctx, upstream, "eth_getBlockByNumber")

	for _, req := range hardhatReqCount {
		if strings.Contains(req.param, "finalized") {
			t.Fatalf("nodecore sent eth_getBlockByNumber with finalized param: %s", req.param)
		}
		if strings.Contains(req.param, "safe") {
			t.Fatalf("nodecore sent eth_getBlockByNumber with safe param: %s", req.param)
		}
	}

	// Head polling via "latest" is still expected.
	hasLatest := false
	for _, req := range hardhatReqCount {
		if strings.Contains(req.param, "latest") {
			hasLatest = true
			break
		}
	}
	if !hasLatest {
		t.Fatal("nodecore did not send any eth_getBlockByNumber with latest — head polling may be broken")
	}

	t.Logf("Viction correctly skipped finalized/safe polling; sent %d eth_getBlockByNumber requests (latest only)", len(hardhatReqCount))
}

func TestEvmLowerBoundsSkipUnsupportedProofDetectorBySpec(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-no-proof", SeedTxs: 1, Rules: unsupportedProofRule()},
	)
	upstream := nodes[0]
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, noProofLowerBoundsNodecoreConfig(upstream, "viction", "hyperliquid"))
	defer nodecore.Terminate(ctx)

	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	waitReady(t, nodecore)

	// Lower-bound detectors have a 15s startup delay. After that delay, any
	// wrongly-created proof detector would call eth_getProof and hit this
	// upstream's -32601 rule.
	harness.ClearHardhatRequests(t, ctx, upstream)
	time.Sleep(20 * time.Second)

	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getProof"); got != 0 {
		t.Fatalf("nodecore sent %d eth_getProof lower-bound probes despite the chain specs disabling it\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

type hardhatRequest struct {
	param string
}

func unsupportedProofRule() string {
	return `[{"method":"eth_getProof","error":{"code":-32601,"message":"the method eth_getProof does not exist/is not available"}}]`
}

func countHardhatRequestsByMethodAndParam(t *testing.T, ctx context.Context, node *harness.RPCNode, method string) []hardhatRequest {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, node.Endpoint, "hardhat_getE2ERequests", []any{})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("get hardhat requests on %s: %+v", node.Alias, resp)
	}
	items, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("unexpected hardhat request log response from %s: %+v", node.Alias, resp)
	}
	var out []hardhatRequest
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || m["method"] != method {
			continue
		}
		params := m["params"]
		out = append(out, hardhatRequest{param: fmt.Sprint(params)})
	}
	return out
}

func noProofLowerBoundsNodecoreConfig(upstream *harness.RPCNode, chains ...string) string {
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
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`)
	for _, chain := range chains {
		out += fmt.Sprintf(`    - id: %s-%s
      chain: %s
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-chain-validation: true
        disable-finalized-block-detection: true
        disable-safe-block-detection: true
        disable-lower-bounds-detection: false
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, chain, chain, upstream.InternalURL())
	}
	return out
}

func victionNodecoreConfig(upstreams ...*harness.RPCNode) string {
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
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`)
	for _, upstream := range upstreams {
		out += fmt.Sprintf(`    - id: %s
      chain: viction
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-chain-validation: true
        disable-finalized-block-detection: true
        disable-safe-block-detection: true
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL())
	}
	return out
}
