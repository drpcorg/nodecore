//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const hardhatAccount = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
const broadcastSender = "0x19e7e376e7c213b7e7e7e46cc70a5dd086daff2a"

func TestHTTPDispatchBroadcastRawTransactionBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-broadcast-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-broadcast-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{Broadcast: true}, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	fundBroadcastSender(t, ctx, nodeA)
	fundBroadcastSender(t, ctx, nodeB)
	nonceA := hardhatNonce(t, ctx, nodeA, broadcastSender)
	nonceB := hardhatNonce(t, ctx, nodeB, broadcastSender)
	if nonceA != nonceB {
		t.Fatalf("forked Hardhat nodes disagree on broadcast sender nonce: %s != %s", nonceA, nonceB)
	}
	rawTx, wantHash := signedHardhatTx(t, nonceA)
	resp := harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_sendRawTransaction", []any{rawTx})
	if errVal, hasErr := resp["error"]; hasErr {
		t.Logf("broadcast returned public error after fanout, verifying external transaction effects anyway: %+v", errVal)
	} else if got := harness.ResultString(t, resp); got != wantHash {
		t.Fatalf("broadcast tx hash mismatch: got %s want %s; response=%+v\nlogs:\n%s", got, wantHash, resp, nodecore.Logs(ctx))
	}

	waitTxOnHardhat(t, ctx, nodeA, wantHash)
	waitTxOnHardhat(t, ctx, nodeB, wantHash)
}

func TestHTTPDispatchBroadcastDisabledUsesSingleUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-broadcast-disabled-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-broadcast-disabled-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	fundBroadcastSender(t, ctx, nodeA)
	fundBroadcastSender(t, ctx, nodeB)
	nonceA := hardhatNonce(t, ctx, nodeA, broadcastSender)
	nonceB := hardhatNonce(t, ctx, nodeB, broadcastSender)
	if nonceA != nonceB {
		t.Fatalf("forked Hardhat nodes disagree on broadcast sender nonce: %s != %s", nonceA, nonceB)
	}
	rawTx, wantHash := signedHardhatTx(t, nonceA)

	nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingConfig("paid", map[string]string{nodeA.Alias: "paid", nodeB.Alias: "free"}, nil, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	resp := harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", "eth_sendRawTransaction", []any{rawTx})
	if _, hasErr := resp["error"]; !hasErr {
		if got := harness.ResultString(t, resp); got != wantHash {
			t.Fatalf("single-route tx hash mismatch: got %s want %s; response=%+v\nlogs:\n%s", got, wantHash, resp, nodecore.Logs(ctx))
		}
	}

	waitTxOnHardhat(t, ctx, nodeA, wantHash)
	assertNoTxOnHardhat(t, ctx, nodeB, wantHash)
}

func TestHTTPDispatchNotNullTransactionByHashBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	direct := harness.HardhatRPC(t, ctx, nodeB.Endpoint, "eth_sendTransaction", []any{map[string]any{
		"from":  hardhatAccount,
		"to":    "0x000000000000000000000000000000000000dead",
		"value": "0x1",
	}})
	txHash := harness.ResultString(t, direct)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{NotNull: true}, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionByHash", []any{txHash})
	if resp["result"] == nil {
		t.Fatalf("not-null returned null for tx existing on second node; response=%+v\nlogs:\n%s", resp, nodecore.Logs(ctx))
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || !equalHex(fmt.Sprint(result["hash"]), txHash) {
		t.Fatalf("not-null returned wrong tx: response=%+v want hash %s\nlogs:\n%s", resp, txHash, nodecore.Logs(ctx))
	}
}

func TestHTTPDispatchNotNullStopsOnFirstNonNullBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-first-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-first-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	direct := harness.HardhatRPC(t, ctx, nodeA.Endpoint, "eth_sendTransaction", []any{map[string]any{
		"from":  hardhatAccount,
		"to":    "0x000000000000000000000000000000000000dead",
		"value": "0x2",
	}})
	txHash := harness.ResultString(t, direct)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{NotNull: true}, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionByHash", []any{txHash})
	result, ok := resp["result"].(map[string]any)
	if !ok || !equalHex(fmt.Sprint(result["hash"]), txHash) {
		t.Fatalf("not-null first non-null returned wrong tx: response=%+v want hash %s\nlogs:\n%s", resp, txHash, nodecore.Logs(ctx))
	}
}

func TestHTTPDispatchNotNullAllNullBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-null-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-null-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{NotNull: true}, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	missingHash := "0x0000000000000000000000000000000000000000000000000000000000000abc"
	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionByHash", []any{missingHash})
	if resp["result"] != nil {
		t.Fatalf("not-null all-null returned non-null result: response=%+v\nlogs:\n%s", resp, nodecore.Logs(ctx))
	}
}

func TestHTTPDispatchNotNullDisabledUsesSingleUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-disabled-a", SeedTxs: 0},
		harness.HardhatNodeSpec{Alias: "hardhat-notnull-disabled-b", SeedTxs: 0},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	direct := harness.HardhatRPC(t, ctx, nodeB.Endpoint, "eth_sendTransaction", []any{map[string]any{
		"from":  hardhatAccount,
		"to":    "0x000000000000000000000000000000000000dead",
		"value": "0x3",
	}})
	txHash := harness.ResultString(t, direct)

	nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingConfig("paid", map[string]string{nodeA.Alias: "paid", nodeB.Alias: "free"}, nil, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionByHash", []any{txHash})
	if resp["result"] != nil {
		t.Fatalf("not-null disabled should use only preferred node and return null; response=%+v\nlogs:\n%s", resp, nodecore.Logs(ctx))
	}
}

func TestHTTPDefaultRoutingReturnsSingleUpstreamResultBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-default-a", SeedTxs: 4},
		harness.HardhatNodeSpec{Alias: "hardhat-default-b", SeedTxs: 8},
	)
	nodeA, nodeB := nodes[0], nodes[1]
	defer nodeA.Terminate(ctx)
	defer nodeB.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{}, nodeA, nodeB))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	got := harness.ResultString(t, resp)
	if got != nodeA.NonceHex && got != nodeB.NonceHex {
		t.Fatalf("default routing returned unexpected value: got %s; want %s or %s; response=%+v\nlogs:\n%s", got, nodeA.NonceHex, nodeB.NonceHex, resp, nodecore.Logs(ctx))
	}
}

func TestHTTPLabelBalancingPreferredGroupBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodes := harness.StartHardhatNodes(t, ctx, networkName,
		harness.HardhatNodeSpec{Alias: "hardhat-label-paid", SeedTxs: 6},
		harness.HardhatNodeSpec{Alias: "hardhat-label-free", SeedTxs: 2},
	)
	paid, free := nodes[0], nodes[1]
	defer paid.Terminate(ctx)
	defer free.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, labelBalancingConfig("paid", map[string]string{paid.Alias: "paid", free.Alias: "free"}, nil, paid, free))
	defer nodecore.Terminate(ctx)

	resp := callUntilResult(t, ctx, nodecore, "eth_getTransactionCount", []any{hardhatAccount, "latest"})
	if got := harness.ResultString(t, resp); got != paid.NonceHex {
		t.Fatalf("label balancing did not prefer paid group: got %s want %s; response=%+v\nlogs:\n%s", got, paid.NonceHex, resp, nodecore.Logs(ctx))
	}
}

func callUntilResult(t *testing.T, ctx context.Context, nodecore *harness.Nodecore, method string, params any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.JSONRPC(t, ctx, nodecore.HTTPURL, "ethereum", method, params)
		if _, hasErr := last["error"]; !hasErr {
			return last
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s did not succeed before timeout; last=%+v\nlogs:\n%s", method, last, nodecore.Logs(ctx))
	return nil
}

type dispatchOptions struct {
	Broadcast    bool
	MaximumValue bool
	NotNull      bool
}

func nodecoreConfig(dispatch dispatchOptions, upstreams ...*harness.RPCNode) string {
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
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: %t
        maximum-value: %t
        not-null: %t
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`, dispatch.Broadcast, dispatch.MaximumValue, dispatch.NotNull)
	for _, upstream := range upstreams {
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
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, upstream.Alias, upstream.InternalURL())
	}
	return out
}

func labelBalancingConfig(order string, labels map[string]string, disabledMethods map[string][]string, upstreams ...*harness.RPCNode) string {
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
  label-balancing:
    order: [%s]
    include-default: false
  chain-defaults:
    ethereum:
      dispatch:
        broadcast: false
        maximum-value: false
        not-null: false
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
`, order)
	for _, upstream := range upstreams {
		out += fmt.Sprintf(`    - id: %s
      chain: ethereum
`, upstream.Alias)
		if label := labels[upstream.Alias]; label != "" {
			out += fmt.Sprintf("      group-labels: [%s]\n", label)
		}
		out += fmt.Sprintf(`      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
`, upstream.InternalURL())
		if disabled := disabledMethods[upstream.Alias]; len(disabled) > 0 {
			out += "      methods:\n        disable:\n"
			for _, method := range disabled {
				out += fmt.Sprintf("          - %q\n", method)
			}
		}
		out += `      options:
        internal-timeout: 5s
        validation-interval: 30s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`
	}
	return out
}

func waitTxOnHardhat(t *testing.T, ctx context.Context, node *harness.RPCNode, hash string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.HardhatRPC(t, ctx, node.Endpoint, "eth_getTransactionByHash", []any{hash})
		if _, hasErr := last["error"]; !hasErr && last["result"] != nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("tx %s not found on %s before timeout; last=%+v", hash, node.Alias, last)
}

func assertNoTxOnHardhat(t *testing.T, ctx context.Context, node *harness.RPCNode, hash string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = harness.HardhatRPC(t, ctx, node.Endpoint, "eth_getTransactionByHash", []any{hash})
		if _, hasErr := last["error"]; hasErr || last["result"] != nil {
			t.Fatalf("tx %s unexpectedly found on %s; last=%+v", hash, node.Alias, last)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func fundBroadcastSender(t *testing.T, ctx context.Context, node *harness.RPCNode) {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, node.Endpoint, "hardhat_setBalance", []any{broadcastSender, "0x3635c9adc5dea00000"})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("fund broadcast sender on %s: %+v", node.Alias, resp)
	}
}

func hardhatNonce(t *testing.T, ctx context.Context, node *harness.RPCNode, account string) string {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, node.Endpoint, "eth_getTransactionCount", []any{account, "latest"})
	return harness.ResultString(t, resp)
}

func signedHardhatTx(t *testing.T, nonceHex string) (raw string, hash string) {
	t.Helper()
	nonce, err := strconv.ParseUint(strings.TrimPrefix(nonceHex, "0x"), 16, 64)
	if err != nil {
		t.Fatalf("parse nonce %q: %v", nonceHex, err)
	}
	key, err := crypto.HexToECDSA("1111111111111111111111111111111111111111111111111111111111111111")
	if err != nil {
		t.Fatalf("parse hardhat private key: %v", err)
	}
	to := common.HexToAddress("0x70997970c51812dc3a010c7d01b50e0d17dc79c8")
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     nonce,
		GasTipCap: big.NewInt(2_000_000_000),
		GasFeeCap: big.NewInt(200_000_000_000),
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(1),
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	bin, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("encode signed tx: %v", err)
	}
	return "0x" + common.Bytes2Hex(bin), signed.Hash().Hex()
}

func equalHex(a, b string) bool { return common.HexToHash(a) == common.HexToHash(b) }
