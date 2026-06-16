//go:build e2e

package grpc_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestGRPCAdvertisedHeadMatchesNativeCallRoutingHeadBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-grpc-head-consistency", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, grpcHeadConsistencyNodecoreConfig(upstream))
	defer nodecore.Terminate(ctx)

	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()

	chain := dshackle.ChainRef(chains.GetChain("ethereum").GrpcId)
	initial := waitForAdvertisedHeadAtLeast(t, ctx, client, nodecore, chain, 1, 45*time.Second)

	harness.HardhatRPC(t, ctx, upstream.Endpoint, "hardhat_mine", []any{"0x3"})
	target := hardhatBlockNumber(t, ctx, upstream)
	if target <= initial {
		t.Fatalf("hardhat_mine did not advance head: initial=%d target=%d", initial, target)
	}

	advertised := waitForAdvertisedHeadAtLeast(t, ctx, client, nodecore, chain, target, 45*time.Second)

	item, err := harness.NativeCallOnce(ctx, client, harness.NativeCall{
		Chain:   chain,
		ID:      901,
		Method:  "eth_getBlockByNumber",
		Payload: fmt.Sprintf(`["0x%x", false]`, advertised),
		Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_HeightSelector{HeightSelector: &dshackle.HeightSelector{
			HeightOrNumber: &dshackle.HeightSelector_Number{Number: advertised},
		}}}},
	}, 10*time.Second)
	if err != nil {
		t.Fatalf("NativeCall for advertised head %d failed: %v\nlogs:\n%s", advertised, err, nodecore.Logs(ctx))
	}
	if !item.GetSucceed() {
		t.Fatalf("NativeCall for advertised head %d was rejected: item=%+v payload=%s error=%s\nlogs:\n%s", advertised, item, string(item.GetPayload()), item.GetErrorMessage(), nodecore.Logs(ctx))
	}
	assertNativeCallBlockNumber(t, item.GetPayload(), advertised)
}

func grpcHeadConsistencyNodecoreConfig(upstream *harness.RPCNode) string {
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
      poll-interval: 500ms
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

func waitForAdvertisedHeadAtLeast(t *testing.T, ctx context.Context, client dshackle.BlockchainClient, nodecore *harness.Nodecore, chain dshackle.ChainRef, min uint64, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last uint64
	for time.Now().Before(deadline) {
		streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		stream, err := client.SubscribeChainStatus(streamCtx, &dshackle.SubscribeChainStatusRequest{})
		if err == nil {
			for {
				resp, recvErr := stream.Recv()
				if recvErr != nil {
					break
				}
				if resp.GetChainDescription().GetChain() != chain {
					continue
				}
				if head, ok := advertisedHead(resp); ok {
					last = head
					if head >= min {
						cancel()
						return head
					}
				}
			}
		}
		cancel()
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("SubscribeChainStatus did not advertise head >= %d before timeout; last=%d\nlogs:\n%s", min, last, nodecore.Logs(ctx))
	return 0
}

func advertisedHead(resp *dshackle.SubscribeChainStatusResponse) (uint64, bool) {
	if resp == nil || resp.GetChainDescription() == nil {
		return 0, false
	}
	for _, event := range resp.GetChainDescription().GetChainEvent() {
		if head := event.GetHead(); head != nil && head.GetHeight() > 0 {
			return head.GetHeight(), true
		}
	}
	return 0, false
}

func hardhatBlockNumber(t *testing.T, ctx context.Context, upstream *harness.RPCNode) uint64 {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, upstream.Endpoint, "eth_blockNumber", []any{})
	return parseHexQuantityE2E(t, harness.ResultString(t, resp))
}

func assertNativeCallBlockNumber(t *testing.T, payload []byte, want uint64) {
	t.Helper()
	var block struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(payload, &block); err != nil {
		t.Fatalf("NativeCall block payload is not a JSON block object: %s err=%v", string(payload), err)
	}
	if got := parseHexQuantityE2E(t, block.Number); got != want {
		t.Fatalf("NativeCall returned wrong block number: got %d want %d payload=%s", got, want, string(payload))
	}
}

func parseHexQuantityE2E(t *testing.T, value string) uint64 {
	t.Helper()
	var out uint64
	if _, err := fmt.Sscanf(value, "0x%x", &out); err != nil {
		t.Fatalf("parse hex quantity %q: %v", value, err)
	}
	return out
}
