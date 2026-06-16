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

func TestHTTPArchiveCapabilitySelectorUsesArchiveHardhatBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-archive-false", SeedTxs: 9},
		harness.HardhatNodeSpec{Alias: "hardhat-archive-true", SeedTxs: 3, Rules: `[
			{"method":"eth_getBalance","result":"0x0"}
		]`},
	)
	nonArchive, archive := nodes[0], nodes[1]
	defer nonArchive.Terminate(ctx)
	defer archive.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, archiveSelectorNodecoreConfig(nonArchive, archive))
	defer nodecore.Terminate(ctx)

	got := grpcNativeCallStringWithArchiveSelector(t, ctx, nodecore, "eth_getTransactionCount", `[`+fmt.Sprintf("%q", hardhatAccount)+`,"latest"]`)
	if got != archive.NonceHex {
		t.Fatalf("archive selector did not route to archive Hardhat upstream: got %s want %s\nlogs:\n%s", got, archive.NonceHex, nodecore.Logs(ctx))
	}
}

func archiveSelectorNodecoreConfig(nonArchive, archive *harness.RPCNode) string {
	out := `server:
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
`
	for _, upstream := range []*harness.RPCNode{nonArchive, archive} {
		archiveOption := "true"
		if upstream == nonArchive {
			archiveOption = "false"
		}
		out += fmt.Sprintf(`    - id: %s
      chain: ethereum
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: false
        archive: %s
        disable-log-index-validation: true
        validate-client-version: false
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL(), archiveOption)
	}
	return out
}

func grpcNativeCallStringWithArchiveSelector(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, method, payload string) string {
	t.Helper()
	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()

	item := harness.NativeCallUntilSuccess(t, ctx, client, nodecore, harness.NativeCall{
		Chain:   dshackle.ChainRef(chains.GetChain("ethereum").GrpcId),
		ID:      801,
		Method:  method,
		Payload: payload,
		Selectors: []*dshackle.Selector{{SelectorType: &dshackle.Selector_LabelSelector{LabelSelector: &dshackle.LabelSelector{
			Name:  "archive",
			Value: []string{"true"},
		}}}},
	}, 75*time.Second)

	var result string
	if err := json.Unmarshal(item.GetPayload(), &result); err != nil {
		t.Fatalf("decode NativeCall payload %q: %v", string(item.GetPayload()), err)
	}
	return result
}
