//go:build e2e

package http_e2e

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

func TestHTTPLiveLowerBoundAvoidsPrunedHardhatProbabilisticBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	const logsParamsJSON = `[{"fromBlock":"0x1000","toBlock":"0x1001"}]`
	logsParams := []any{map[string]any{"fromBlock": "0x1000", "toBlock": "0x1001"}}

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-lower-pruned", SeedTxs: 1, Rules: prunedLogsRule()},
		harness.HardhatNodeSpec{Alias: "hardhat-lower-archive", SeedTxs: 2, Rules: archiveLogsRule()},
	)
	pruned, archive := nodes[0], nodes[1]
	defer pruned.Terminate(ctx)
	defer archive.Terminate(ctx)

	scorePath := "/tmp/nodecore-e2e-lower-bound-score.js"
	nodecore := harness.StartNodecoreWithFiles(t, ctx, networkName, lowerBoundNodecoreConfig(scorePath, pruned, archive), fixedOrderScorePolicyFiles(scorePath, pruned, archive))
	defer nodecore.Terminate(ctx)

	// Let head polling initialize currentHead and let startup lower-bound detection
	// finish first; the live pruned-error update below must be the last value
	// written for the pruned logs bound.
	_ = callUntilResult(t, ctx, nodecore, "eth_blockNumber", []any{})
	time.Sleep(18 * time.Second)

	first := callUntilResult(t, ctx, nodecore, "eth_getLogs", logsParams)
	if result, ok := first["result"].([]any); !ok || len(result) != 0 {
		t.Fatalf("initial getLogs did not fall back to archive empty result: %+v\nlogs:\n%s", first, nodecore.Logs(ctx))
	}

	harness.ClearHardhatRequests(t, ctx, pruned)

	for i := 0; i < 10; i++ {
		grpcNativeCallLogsWithLowerBoundSelector(t, ctx, nodecore, logsParamsJSON)
	}

	if got := harness.CountHardhatRequests(t, ctx, pruned, "eth_getLogs"); got != 0 {
		t.Fatalf("pruned upstream received %d eth_getLogs calls after live lower-bound update; want 0\nnodecore logs:\n%s", got, nodecore.Logs(ctx))
	}
}

func prunedLogsRule() string {
	return `[{"method":"eth_getLogs","error":{"code":-32000,"message":"missing trie node: history has been pruned"}}]`
}

func archiveLogsRule() string {
	return `[{"method":"eth_getLogs","result":[]}]`
}

func lowerBoundNodecoreConfig(scorePath string, pruned, archive *harness.RPCNode) string {
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
  failsafe-config:
    retry:
      attempts: 2
      delay: 50ms
      max-delay: 50ms
      jitter: 1ms
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 100ms
    calculation-function-file-path: %q
  upstreams:
`, scorePath)
	for _, upstream := range []*harness.RPCNode{pruned, archive} {
		out += fmt.Sprintf(`    - id: %s
      chain: ethereum
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: false
        disable-labels-detection: true
        disable-log-index-validation: true
        validate-client-version: false
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL())
	}
	return out
}

func grpcNativeCallLogsWithLowerBoundSelector(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, paramsJSON string) {
	t.Helper()
	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()

	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{
		Chain:   dshackle.ChainRef(chains.GetChain("ethereum").GrpcId),
		ID:      901,
		Method:  "eth_getLogs",
		Payload: paramsJSON,
		Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_LowerHeightSelector{LowerHeightSelector: &dshackle.LowerHeightSelector{
			Height:         0x1000,
			LowerBoundType: dshackle.LowerBoundType_LOWER_BOUND_LOGS,
		}}}},
	}, 45*time.Second)

	var logs []any
	if err := json.Unmarshal(item.GetPayload(), &logs); err != nil {
		t.Fatalf("gRPC NativeCall eth_getLogs returned non-log payload: item=%+v err=%v\nlogs:\n%s", item, err, nodecore.Logs(ctx))
	}
}
