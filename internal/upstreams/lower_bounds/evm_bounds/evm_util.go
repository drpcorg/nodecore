package evm_bounds

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
)

// firstTxHash fetches a block and returns its first transaction hash, used by the
// tx probe. It handles both eth_getBlockByNumber transaction shapes
// (array of hash strings, or array of tx objects).
func (e *EvmLowerBoundDetector) firstTxHash(ctx context.Context, height int64) (string, bool, error) {
	raw, available, err := e.call(ctx, "eth_getBlockByNumber", []any{evmBlockTag(height), false})
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

// lastTxHash fetches a block and returns its last transaction hash, used by the receipts
// probe. empty reports a block that exists but has no transactions to probe.
func (e *EvmLowerBoundDetector) lastTxHash(ctx context.Context, height int64) (hash string, available, empty bool, err error) {
	raw, available, err := e.call(ctx, "eth_getBlockByNumber", []any{evmBlockTag(height), false})
	if err != nil || !available || isEvmNullResult(raw) {
		return "", false, false, err
	}
	block := evmBlockEnvelope{}
	if err := sonic.Unmarshal(raw, &block); err != nil {
		return "", false, false, fmt.Errorf("EVM upstream '%s' block %d unparseable: %w", e.UpstreamId, height, err)
	}
	if len(block.Transactions) == 0 {
		return "", false, true, nil
	}
	tx, ok := txRefHash(block.Transactions[len(block.Transactions)-1])
	return tx, ok, false, nil
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
	return txRefHash(b.Transactions[0])
}

func txRefHash(raw json.RawMessage) (string, bool) {
	ref := strings.TrimSpace(string(raw))
	if ref == "" || ref == "null" {
		return "", false
	}
	if strings.HasPrefix(ref, `"`) {
		var hash string
		if err := sonic.Unmarshal(raw, &hash); err == nil && hash != "" {
			return hash, true
		}
		return "", false
	}
	tx := evmTxRef{}
	if err := sonic.Unmarshal(raw, &tx); err != nil || tx.Hash == "" {
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

// parseEvmBlockNumber accepts both JSON shapes a block number is reported in: a quoted hex
// string ("0x1a") and a bare decimal number (26). parseHexInt alone would read a bare 26
// as 0x26.
func parseEvmBlockNumber(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		return parseHexInt(raw)
	}
	return strconv.ParseInt(trimmed, 10, 64)
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
