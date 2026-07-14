package aptos_validations

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

var errAptosEmptyLedgerInfo = errors.New("aptos node returned empty ledger info")

// AptosLedgerInfo is the GET /v1 (index) payload. All U64 fields are JSON
// strings; chain_id is a JSON number.
type AptosLedgerInfo struct {
	ChainId             uint64 `json:"chain_id"`
	Epoch               string `json:"epoch"`
	LedgerVersion       string `json:"ledger_version"`
	OldestLedgerVersion string `json:"oldest_ledger_version"`
	LedgerTimestamp     string `json:"ledger_timestamp"`
	NodeRole            string `json:"node_role"`
	OldestBlockHeight   string `json:"oldest_block_height"`
	BlockHeight         string `json:"block_height"`
	GitHash             string `json:"git_hash"`
}

// FetchLedgerInfo issues GET /v1 and parses the payload. Callers that need a
// deadline wrap ctx themselves.
func FetchLedgerInfo(ctx context.Context, connector connectors.ApiConnector, chain chains.Chain) (*AptosLedgerInfo, error) {
	request := protocol.NewInternalUpstreamRestRequest("GET#/v1", nil, chain)
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, errAptosEmptyLedgerInfo
	}
	var info AptosLedgerInfo
	if err := sonic.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("couldn't parse aptos ledger info: %w", err)
	}
	return &info, nil
}

// ParseU64 parses an Aptos string-encoded U64 field, returning 0 on empty.
func ParseU64(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}
