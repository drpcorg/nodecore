//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const httpAuthKey = "nodecore-e2e-key"

func TestHTTPAuthLocalKeyRequiredBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startAuthHardhat(t, ctx, networkName, "hardhat-auth-required")
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, httpAuthNodecoreConfig(upstream, nil), httpAuthKey)
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, upstream)
	harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_blockNumber", []any{}, nil)
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_blockNumber"); got != 0 {
		t.Fatalf("unauthenticated request reached upstream: eth_blockNumber calls=%d want 0\nlogs:\n%s", got, nodecore.Logs(ctx))
	}

	resp := harness.JSONRPCWithHeaders(t, ctx, nodecore.HTTPURL, "ethereum", "eth_blockNumber", []any{}, map[string]string{"X-Nodecore-Key": httpAuthKey})
	if got := harness.ResultString(t, resp); got != "0x777" {
		t.Fatalf("authenticated response mismatch: got %s want 0x777; response=%+v\nlogs:\n%s", got, resp, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_blockNumber"); got != 1 {
		t.Fatalf("authenticated request should reach upstream once: eth_blockNumber calls=%d want 1\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func TestHTTPAuthLocalKeyURLPathBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startAuthHardhat(t, ctx, networkName, "hardhat-auth-path")
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, httpAuthNodecoreConfig(upstream, nil), httpAuthKey)
	defer nodecore.Terminate(ctx)

	resp := harness.JSONRPCWithHeaders(t, ctx, nodecore.HTTPURL, "ethereum/api-key/"+httpAuthKey, "eth_blockNumber", []any{}, nil)
	if got := harness.ResultString(t, resp); got != "0x777" {
		t.Fatalf("api-key path response mismatch: got %s want 0x777; response=%+v\nlogs:\n%s", got, resp, nodecore.Logs(ctx))
	}
	harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum/api-key/wrong-key", "eth_blockNumber", []any{}, nil)
}

func TestHTTPAuthMethodRestrictionBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startAuthHardhat(t, ctx, networkName, "hardhat-auth-methods")
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, httpAuthNodecoreConfig(upstream, []string{"eth_blockNumber"}), httpAuthKey)
	defer nodecore.Terminate(ctx)

	headers := map[string]string{"X-Nodecore-Key": httpAuthKey}
	harness.ClearHardhatRequests(t, ctx, upstream)
	allowed := harness.JSONRPCWithHeaders(t, ctx, nodecore.HTTPURL, "ethereum", "eth_blockNumber", []any{}, headers)
	if got := harness.ResultString(t, allowed); got != "0x777" {
		t.Fatalf("allowed method response mismatch: got %s want 0x777; response=%+v\nlogs:\n%s", got, allowed, nodecore.Logs(ctx))
	}
	denied := harness.JSONRPCExpectError(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getTransactionCount", []any{hardhatAccount, "latest"}, headers)
	if denied["error"] == nil {
		t.Fatalf("denied method response missing error: %+v\nlogs:\n%s", denied, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getTransactionCount"); got != 0 {
		t.Fatalf("denied method reached upstream: eth_getTransactionCount calls=%d want 0\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func startAuthHardhat(t *testing.T, ctx context.Context, networkName, alias string) *harness.HardhatNode {
	t.Helper()
	nodes := harness.StartHardhatNodes(t, ctx, networkName, harness.HardhatNodeSpec{Alias: alias, Rules: `[
		{"method":"eth_blockNumber","result":"0x777"}
	]`})
	return nodes[0]
}

func httpAuthNodecoreConfig(upstream *harness.RPCNode, allowedMethods []string) string {
	settings := ""
	if len(allowedMethods) > 0 {
		settings = "        settings:\n          methods:\n            allowed:\n"
		for _, method := range allowedMethods {
			settings += fmt.Sprintf("              - %q\n", method)
		}
	}
	return fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
auth:
  enabled: true
  key-management:
    - id: local-e2e
      type: local
      local:
        key: %q
%supstream-config:
  mode: default
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
    - id: %s
      chain: ethereum
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
`, httpAuthKey, settings, upstream.Alias, upstream.InternalURL())
}
