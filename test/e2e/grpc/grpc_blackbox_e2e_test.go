//go:build e2e

package grpc_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const (
	defaultChain      = "ethereum"
	defaultRPCTimeout = 45 * time.Second
)

func TestGrpcBlackbox(t *testing.T) {
	ctx := context.Background()
	chainName, chain, endpoint, redaction := grpcBlackboxChainConfig(t)

	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodecore := harness.StartNodecore(t, ctx, networkName, grpcBlackboxNodecoreConfig(chainName, endpoint), redaction)
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	chainRef := dshackle.ChainRef(chain.GrpcId)

	harness.AssertFullChainStatus(t, harness.WaitForFullChainStatus(t, ctx, client, nodecore, chainRef, defaultRPCTimeout))

	t.Run("native_call", func(t *testing.T) {
		testNativeCall(t, ctx, client, nodecore, chainRef)
	})

	t.Run("chain_status", func(t *testing.T) {
		harness.AssertFullChainStatus(t, harness.WaitForFullChainStatus(t, ctx, client, nodecore, chainRef, defaultRPCTimeout))
	})

	t.Run("native_call_negative_cases", func(t *testing.T) {
		testNativeCallNegativeCases(t, ctx, client, chainRef)
	})
}

func grpcBlackboxChainConfig(t *testing.T) (chainName string, chain *chains.ConfiguredChain, endpoint string, redaction string) {
	t.Helper()

	drpcKey := strings.TrimSpace(os.Getenv("NODECORE_E2E_DRPC_KEY"))
	if drpcKey == "" {
		t.Fatal("NODECORE_E2E_DRPC_KEY is required for blackbox gRPC e2e tests")
	}

	chainName = strings.TrimSpace(os.Getenv("NODECORE_E2E_CHAIN"))
	if chainName == "" {
		chainName = defaultChain
	}
	chain = chains.GetChain(chainName)
	if chain == nil || chain.Chain < 0 {
		t.Fatalf("unsupported NODECORE_E2E_CHAIN %q", chainName)
	}

	return chainName, chain, drpcEndpoint(chainName, drpcKey), drpcKey
}

func testNativeCall(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *harness.Nodecore, chain dshackle.ChainRef) {
	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{Chain: chain, ID: 1, Method: "eth_blockNumber", Payload: `[]`}, defaultRPCTimeout)
	if item.GetId() != 1 {
		t.Fatalf("NativeCall id mismatch: got %d want 1; item=%+v", item.GetId(), item)
	}
	if item.GetUpstreamId() == "" {
		t.Fatalf("NativeCall response has empty upstream id: %+v", item)
	}

	gotBlockNumber := decodeJSONRPCStringResult(t, item.GetPayload())
	if !isHexQuantity(gotBlockNumber) {
		t.Fatalf("NativeCall eth_blockNumber returned invalid hex quantity: got %q payload=%q", gotBlockNumber, string(item.GetPayload()))
	}
}

func testNativeCallNegativeCases(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, chain dshackle.ChainRef) {
	t.Run("unsupported_chain", func(t *testing.T) {
		item, err := harness.NativeCallOnce(ctx, client, harness.NativeCall{Chain: dshackle.ChainRef(99999999), ID: 11, Method: "eth_chainId", Payload: `[]`}, defaultRPCTimeout)
		if err != nil {
			t.Fatalf("NativeCall unsupported chain failed: %v", err)
		}
		if item.GetSucceed() {
			t.Fatalf("NativeCall unsupported chain unexpectedly succeeded: %+v", item)
		}
		if item.GetErrorMessage() == "" {
			t.Fatalf("NativeCall unsupported chain error message is empty: %+v", item)
		}
	})

	t.Run("malformed_payload", func(t *testing.T) {
		item, err := harness.NativeCallOnce(ctx, client, harness.NativeCall{Chain: chain, ID: 12, Method: "eth_call", Payload: `not-json`}, defaultRPCTimeout)
		if err != nil {
			t.Fatalf("NativeCall malformed payload failed: %v", err)
		}
		if item.GetSucceed() {
			t.Fatalf("NativeCall malformed payload unexpectedly succeeded: %+v", item)
		}
		if item.GetId() != 12 {
			t.Fatalf("NativeCall malformed payload id mismatch: got %d want 12", item.GetId())
		}
		if item.GetErrorMessage() == "" {
			t.Fatalf("NativeCall malformed payload error message is empty: %+v", item)
		}
	})
}

func grpcBlackboxNodecoreConfig(chainName, endpoint string) string {
	return fmt.Sprintf(`server:
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
    - id: drpc-e2e-%s
      chain: %s
      poll-interval: 2s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 15s
        validation-interval: 15s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, chainName, chainName, endpoint)
}

func drpcEndpoint(chainName, key string) string {
	envName := "NODECORE_E2E_DRPC_ENDPOINT_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(chainName))
	if endpoint := strings.TrimSpace(os.Getenv(envName)); endpoint != "" {
		return endpoint
	}
	return fmt.Sprintf("https://lb.drpc.live/%s/%s", url.PathEscape(chainName), url.PathEscape(key))
}

func decodeJSONRPCStringResult(t *testing.T, payload []byte) string {
	t.Helper()
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		t.Fatal("empty JSON-RPC result payload")
	}

	var result string
	if err := json.Unmarshal(payload, &result); err == nil {
		return result
	}
	return trimmed
}

func isHexQuantity(value string) bool {
	if len(value) < 3 || !strings.HasPrefix(value, "0x") {
		return false
	}
	for _, r := range value[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
