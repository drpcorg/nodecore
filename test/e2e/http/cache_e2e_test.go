//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

const cacheBalanceAccount = "0x0000000000000000000000000000000000000001"

func TestHTTPCacheMemoryHitAvoidsSecondUpstreamCallBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startCacheHardhat(t, ctx, networkName, "hardhat-cache-hit", `[{
		"method":"eth_getBalance",
		"result":"0xabc"
	}]`)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, memoryCacheNodecoreConfig(upstream, "eth_getBalance", "30s", "1s"))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, upstream)
	params := []any{cacheBalanceAccount, "0x1"}
	first := harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)
	if got := harness.ResultString(t, first); got != "0xabc" {
		t.Fatalf("first cacheable response mismatch: got %s want 0xabc; response=%+v\nlogs:\n%s", got, first, nodecore.Logs(ctx))
	}
	second := harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)
	if got := harness.ResultString(t, second); got != "0xabc" {
		t.Fatalf("second cacheable response mismatch: got %s want 0xabc; response=%+v\nlogs:\n%s", got, second, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getBalance"); got != 1 {
		t.Fatalf("cache hit should avoid second upstream call: hardhat eth_getBalance calls=%d want 1\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func TestHTTPCacheMemoryTTLExpiryBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startCacheHardhat(t, ctx, networkName, "hardhat-cache-ttl", `[{
		"method":"eth_getBalance",
		"result":"0xdef"
	}]`)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, memoryCacheNodecoreConfig(upstream, "eth_getBalance", "300ms", "100ms"))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, upstream)
	params := []any{cacheBalanceAccount, "0x2"}
	if got := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)); got != "0xdef" {
		t.Fatalf("first ttl response mismatch: got %s want 0xdef\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
	if got := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)); got != "0xdef" {
		t.Fatalf("cached ttl response mismatch: got %s want 0xdef\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getBalance"); got != 1 {
		t.Fatalf("immediate ttl cache hit should keep upstream calls at 1, got %d\nlogs:\n%s", got, nodecore.Logs(ctx))
	}

	time.Sleep(900 * time.Millisecond)
	if got := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)); got != "0xdef" {
		t.Fatalf("post-ttl response mismatch: got %s want 0xdef\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getBalance"); got != 2 {
		t.Fatalf("post-ttl request should hit upstream again: hardhat eth_getBalance calls=%d want 2\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func TestHTTPCacheDoesNotCacheLatestBlockTagBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startCacheHardhat(t, ctx, networkName, "hardhat-cache-latest", `[{
		"method":"eth_getBalance",
		"result":"0x456"
	}]`)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, memoryCacheNodecoreConfig(upstream, "eth_getBalance", "30s", "1s"))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, upstream)
	params := []any{cacheBalanceAccount, "latest"}
	for i := 0; i < 2; i++ {
		if got := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_getBalance", params)); got != "0x456" {
			t.Fatalf("latest response %d mismatch: got %s want 0x456\nlogs:\n%s", i+1, got, nodecore.Logs(ctx))
		}
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_getBalance"); got != 2 {
		t.Fatalf("latest block-tag requests must bypass cache: hardhat eth_getBalance calls=%d want 2\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func TestHTTPCacheDoesNotCacheNonCacheableMethodBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := startCacheHardhat(t, ctx, networkName, "hardhat-cache-noncacheable", `[{
		"method":"eth_sendRawTransaction",
		"result":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}]`)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, memoryCacheNodecoreConfig(upstream, "*", "30s", "1s"))
	defer nodecore.Terminate(ctx)

	harness.ClearHardhatRequests(t, ctx, upstream)
	params := []any{"0x1234"}
	for i := 0; i < 2; i++ {
		if got := harness.ResultString(t, harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_sendRawTransaction", params)); got != "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatalf("non-cacheable response %d mismatch: got %s\nlogs:\n%s", i+1, got, nodecore.Logs(ctx))
		}
	}
	if got := harness.CountHardhatRequests(t, ctx, upstream, "eth_sendRawTransaction"); got != 2 {
		t.Fatalf("non-cacheable method should hit upstream twice: hardhat eth_sendRawTransaction calls=%d want 2\nlogs:\n%s", got, nodecore.Logs(ctx))
	}
}

func startCacheHardhat(t *testing.T, ctx context.Context, networkName, alias, rules string) *harness.HardhatNode {
	t.Helper()
	nodes := harness.StartHardhatNodes(t, ctx, networkName, harness.HardhatNodeSpec{Alias: alias, Rules: rules})
	return nodes[0]
}

func memoryCacheNodecoreConfig(upstream *harness.RPCNode, methodPattern, ttl, expiredRemoveInterval string) string {
	return fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
cache:
  connectors:
    - id: memory-cache
      driver: memory
      memory:
        max-items: 1000
        expired-remove-interval: %s
  policies:
    - id: memory-policy
      chain: ethereum
      method: %q
      connector-id: memory-cache
      finalization-type: none
      cache-empty: true
      object-max-size: "1000KB"
      ttl: %s
upstream-config:
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
`, expiredRemoveInterval, methodPattern, ttl, upstream.Alias, upstream.InternalURL())
}
