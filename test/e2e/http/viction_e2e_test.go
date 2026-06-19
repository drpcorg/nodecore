//go:build e2e

package http_e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

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
	// should NOT reach the upstream. The exact behaviour depends on whether
	// the flow-level block-tag rejection is implemented:
	//   - If rejected at flow level: -32602 "unsupported block tag"
	//   - If passed through: hardhat returns latest block
	// Either way, the request must not crash nodecore.
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

	// The request must not crash nodecore. We log the response for now;
	// once the flow-level tag-rejection is implemented, assert -32602 here.
	t.Logf("Viction finalized tag response: %s", string(respBody))
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
