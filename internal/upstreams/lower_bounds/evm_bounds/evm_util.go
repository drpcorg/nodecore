package evm_bounds

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

// firstTxHash fetches a block and returns its first transaction hash, used by the
// tx and receipts probes. It handles both eth_getBlockByNumber transaction shapes
// (array of hash strings, or array of tx objects).
func (e *EvmLowerBoundDetector) firstTxHash(height int64) (string, bool, error) {
	raw, available, err := e.call("eth_getBlockByNumber", []any{evmBlockTag(height), false})
	if err != nil || !available || isEvmNullResult(raw) {
		return "", available && !isEvmNullResult(raw), err
	}
	block := evmBlockEnvelope{}
	if err := sonic.Unmarshal(raw, &block); err != nil {
		return "", false, fmt.Errorf("EVM upstream '%s' block %d unparseable: %w", e.UpstreamId, height, err)
	}
	tx, ok := block.firstTxHash()
	if !ok {
		return "", false, nil
	}
	return tx, true, nil
}

type evmBlockEnvelope struct {
	Number       string            `json:"number"`
	Hash         string            `json:"hash"`
	Transactions []json.RawMessage `json:"transactions"`
}

type evmTxRef struct {
	Hash string `json:"hash"`
}

func (b evmBlockEnvelope) firstTxHash() (string, bool) {
	if len(b.Transactions) == 0 {
		return "", false
	}
	first := strings.TrimSpace(string(b.Transactions[0]))
	if first == "" || first == "null" {
		return "", false
	}
	if strings.HasPrefix(first, `"`) {
		var hash string
		if err := sonic.Unmarshal(b.Transactions[0], &hash); err == nil && hash != "" {
			return hash, true
		}
		return "", false
	}
	tx := evmTxRef{}
	if err := sonic.Unmarshal(b.Transactions[0], &tx); err != nil || tx.Hash == "" {
		return "", false
	}
	return tx.Hash, true
}

func evmBlockTag(height int64) string {
	return fmt.Sprintf("0x%x", height)
}

func parseHexInt(raw []byte) (int64, error) {
	var hexValue string
	if err := sonic.Unmarshal(raw, &hexValue); err != nil {
		hexValue = strings.Trim(string(raw), `"`)
	}
	hexValue = strings.TrimSpace(hexValue)
	hexValue = strings.TrimPrefix(hexValue, "0x")
	if hexValue == "" {
		return 0, fmt.Errorf("empty hex value")
	}
	return strconv.ParseInt(hexValue, 16, 64)
}

func isEvmNullResult(raw []byte) bool {
	return strings.TrimSpace(string(raw)) == "null" || len(strings.TrimSpace(string(raw))) == 0
}

func isEvmEmptyHexResult(raw []byte) bool {
	var value string
	if err := sonic.Unmarshal(raw, &value); err != nil {
		value = strings.Trim(string(raw), `"`)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || value == "0x" || value == "0x0" || value == "null"
}
