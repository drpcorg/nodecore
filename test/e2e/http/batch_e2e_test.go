//go:build e2e

package http_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPBatchJSONRPCMixedResultsBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-batch", 0)
	defer upstream.Terminate(ctx)

	rules := `[
		{"method":"eth_blockNumber","result":"0x123"},
		{"method":"eth_e2eScriptedError","error":{"code":-32001,"message":"scripted batch error"}}
	]`
	setHardhatRulesForBatch(t, ctx, upstream, rules)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{}, upstream))
	defer nodecore.Terminate(ctx)

	batch := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "eth_blockNumber", "params": []any{}},
		{"jsonrpc": "2.0", "id": 2, "method": "eth_e2eScriptedError", "params": []any{}},
		{"jsonrpc": "2.0", "id": 3, "method": "eth_chainId", "params": []any{}},
	}
	responses := harness.JSONRPCBatch(t, ctx, nodecore.HTTPURL, "ethereum", batch, nil)
	if len(responses) != len(batch) {
		t.Fatalf("batch response count mismatch: got %d want %d responses=%+v\nlogs:\n%s", len(responses), len(batch), responses, nodecore.Logs(ctx))
	}

	byID := batchResponsesByID(t, responses)
	if got := byID["1"]["result"]; got != "0x123" {
		t.Fatalf("batch id=1 result mismatch: got %#v want 0x123 responses=%+v\nlogs:\n%s", got, responses, nodecore.Logs(ctx))
	}
	if errVal := byID["2"]["error"]; errVal == nil {
		t.Fatalf("batch id=2 expected error response: responses=%+v\nlogs:\n%s", responses, nodecore.Logs(ctx))
	}
	if got := byID["3"]["result"]; got != "0x1" {
		t.Fatalf("batch id=3 local eth_chainId result mismatch: got %#v want 0x1 responses=%+v\nlogs:\n%s", got, responses, nodecore.Logs(ctx))
	}
}

func TestHTTPBatchJSONRPCEmptyAndInvalidBatchBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-batch-invalid", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{}, upstream))
	defer nodecore.Terminate(ctx)

	empty := harness.JSONRPCRaw(t, ctx, nodecore.HTTPURL, "ethereum", []byte(`[]`), nil)
	if empty.StatusCode != http.StatusOK {
		t.Fatalf("empty batch status=%d want 200 body=%s\nlogs:\n%s", empty.StatusCode, string(empty.Body), nodecore.Logs(ctx))
	}
	var emptyOut []any
	if err := json.Unmarshal(empty.Body, &emptyOut); err != nil {
		t.Fatalf("empty batch did not return valid JSON array: status=%d body=%s err=%v\nlogs:\n%s", empty.StatusCode, string(empty.Body), err, nodecore.Logs(ctx))
	}
	if len(emptyOut) != 0 {
		t.Fatalf("empty batch response len=%d want 0 body=%s\nlogs:\n%s", len(emptyOut), string(empty.Body), nodecore.Logs(ctx))
	}

	invalid := harness.JSONRPCRaw(t, ctx, nodecore.HTTPURL, "ethereum", []byte(`[1]`), nil)
	if invalid.StatusCode != http.StatusOK && invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid batch status=%d want 200 or 400 body=%s\nlogs:\n%s", invalid.StatusCode, string(invalid.Body), nodecore.Logs(ctx))
	}
	var invalidOut map[string]any
	if err := json.Unmarshal(invalid.Body, &invalidOut); err != nil {
		t.Fatalf("invalid batch did not return valid JSON error object: status=%d body=%s err=%v\nlogs:\n%s", invalid.StatusCode, string(invalid.Body), err, nodecore.Logs(ctx))
	}
	if invalidOut["error"] == nil {
		t.Fatalf("invalid batch response missing error: status=%d body=%s\nlogs:\n%s", invalid.StatusCode, string(invalid.Body), nodecore.Logs(ctx))
	}
}

func setHardhatRulesForBatch(t *testing.T, ctx context.Context, upstream *harness.RPCNode, rules string) {
	t.Helper()
	resp := harness.HardhatRPC(t, ctx, upstream.Endpoint, "hardhat_setE2ERules", []any{mustUnmarshalBatchRules(t, rules)})
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("set hardhat rules on %s: %+v", upstream.Alias, resp)
	}
}

func mustUnmarshalBatchRules(t *testing.T, rules string) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal([]byte(rules), &out); err != nil {
		t.Fatalf("parse batch hardhat rules: %v", err)
	}
	return out
}

func batchResponsesByID(t *testing.T, responses []map[string]any) map[string]map[string]any {
	t.Helper()
	byID := make(map[string]map[string]any, len(responses))
	for _, resp := range responses {
		id := fmt.Sprint(resp["id"])
		if id == "<nil>" || id == "" {
			t.Fatalf("batch response missing id: %+v", responses)
		}
		byID[id] = resp
	}
	for _, id := range []string{"1", "2", "3"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("batch response missing id=%s: %+v", id, responses)
		}
	}
	return byID
}
