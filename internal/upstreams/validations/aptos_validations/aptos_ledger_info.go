package aptos_validations

import "strconv"

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

// ParseU64 parses an Aptos string-encoded U64 field, returning 0 on empty.
func ParseU64(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}
