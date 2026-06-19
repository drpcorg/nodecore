//go:build e2e

package http_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "eth_blockNumber",
		"params":  []interface{}{},
		"id":      1,
	}
	reqBody, err := json.Marshal(rpcReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(nodecore.HTTPURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST %s: %v", nodecore.HTTPURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var rpcResp struct {
		Result string `json:"result"`
		Error  any    `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		t.Fatalf("unmarshal response %q: %v", string(respBody), err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("RPC error: %v", rpcResp.Error)
	}
	if rpcResp.Result == "" {
		t.Fatalf("expected hex block number, got empty result: %s", string(respBody))
	}

	t.Logf("Viction nodecore healthy, eth_blockNumber = %s", rpcResp.Result)
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
	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "eth_getBlockByNumber",
		"params":  []interface{}{"finalized", false},
		"id":      1,
	}
	reqBody, err := json.Marshal(rpcReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(nodecore.HTTPURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST %s: %v", nodecore.HTTPURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var rpcResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		t.Fatalf("unmarshal response %q: %v", string(respBody), err)
	}
	if rpcResp.Error.Code != -32602 {
		t.Fatalf("expected -32602 for finalized tag on Viction, got code=%d message=%q body=%s", rpcResp.Error.Code, rpcResp.Error.Message, string(respBody))
	}
	t.Logf("Viction correctly rejects finalized tag: %s", rpcResp.Error.Message)
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

type hardhatRequest struct {
	param string
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
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL())
	}
	return out
}
