package evm_bounds

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const (
	evmLowerBoundPeriod = 3 * time.Minute

	evmZeroAddress = "0x0000000000000000000000000000000000000000"

	stateCheckerAddress  = "0x1111111111111111111111111111111111111111"
	stateCheckerCallData = "0x1eaf190c"
	stateCheckerBytecode = "0x6080604052348015600e575f5ffd5b50600436106026575f3560e01c80631eaf190c14602a575b5f5ffd5b60306044565b604051603b91906078565b60405180910390f35b5f5f73ffffffffffffffffffffffffffffffffffffffff1631905090565b5f819050919050565b6072816062565b82525050565b5f60208201905060895f830184606b565b9291505056fea2646970667358221220251f5b4d2ed1abe77f66fde198a57ada08562dc3b0afbc6bac0261d1bf516b5d64736f6c634300081e0033"
)

type evmStateOverrideSupport int32

const (
	evmStateOverrideUnknown evmStateOverrideSupport = iota
	evmStateOverrideSupported
	evmStateOverrideUnsupported
)

type EvmLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration

	stateOverrideSupport atomic.Int32
}

func NewEvmBlockLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, protocol.BlockBound, []protocol.LowerBoundType{protocol.BlockBound, protocol.LogsBound})
}

func NewEvmStateLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, protocol.StateBound, []protocol.LowerBoundType{protocol.StateBound, protocol.TraceBound})
}

func NewEvmTxLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetector(upstreamId, chain, internalTimeout, connector, protocol.TxBound)
}

func NewEvmReceiptsLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetector(upstreamId, chain, internalTimeout, connector, protocol.ReceiptsBound)
}

func NewEvmProofLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, protocol.ProofBound, []protocol.LowerBoundType{protocol.ProofBound})
}

func newEvmLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
	boundType protocol.LowerBoundType,
) *EvmLowerBoundDetector {
	return newEvmLowerBoundDetectorWithSupportedTypes(upstreamId, chain, internalTimeout, connector, boundType, []protocol.LowerBoundType{boundType})
}

func newEvmLowerBoundDetectorWithSupportedTypes(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
	boundType protocol.LowerBoundType,
	supportedTypes []protocol.LowerBoundType,
) *EvmLowerBoundDetector {
	return &EvmLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithSupportedTypes(upstreamId, boundType, supportedTypes, evmLowerBoundPeriod),
		connector:                  connector,
		chain:                      chain,
		internalTimeout:            internalTimeout,
	}
}

func (e *EvmLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	return e.LowerBoundSearchCalculator.DetectLowerBound(e.fetchLatestHeight, e.probe)
}

func (e *EvmLowerBoundDetector) probe(height int64) (bool, error) {
	switch e.MainBoundType {
	case protocol.StateBound:
		return e.hasState(height)
	case protocol.BlockBound:
		return e.hasBlock(height)
	case protocol.TxBound:
		return e.hasTx(height)
	case protocol.ReceiptsBound:
		return e.hasReceipts(height)
	case protocol.ProofBound:
		return e.hasProof(height)
	default:
		return false, fmt.Errorf("unsupported EVM lower-bound type %s", e.MainBoundType.String())
	}
}

func (e *EvmLowerBoundDetector) fetchLatestHeight() (int64, error) {
	raw, available, err := e.call("eth_blockNumber", []any{})
	if err != nil {
		return 0, err
	}
	if !available {
		return 0, fmt.Errorf("eth_blockNumber unavailable")
	}
	return parseHexInt(raw)
}

func (e *EvmLowerBoundDetector) hasBlock(height int64) (bool, error) {
	raw, available, err := e.call("eth_getBlockByNumber", []any{evmBlockTag(height), false})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasState(height int64) (bool, error) {
	if e.supportsStateOverride() {
		available, err := e.hasStateWithOverride(height)
		if err == nil && available {
			return true, nil
		}
		// Match dshackle behavior: state override is preferred, but a failed
		// per-block override probe falls back to eth_getBalance.
	}
	return e.hasStateWithBalance(height)
}

func (e *EvmLowerBoundDetector) hasStateWithOverride(height int64) (bool, error) {
	raw, available, err := e.call("eth_call", stateOverrideParams(evmBlockTag(height)))
	if err != nil || !available {
		return available, err
	}
	return !isEvmEmptyHexResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasStateWithBalance(height int64) (bool, error) {
	raw, available, err := e.call("eth_getBalance", []any{evmZeroAddress, evmBlockTag(height)})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasTx(height int64) (bool, error) {
	txHash, available, err := e.firstTxHash(height)
	if err != nil || !available {
		return available, err
	}
	raw, available, err := e.call("eth_getTransactionByHash", []any{txHash})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasReceipts(height int64) (bool, error) {
	txHash, available, err := e.firstTxHash(height)
	if err != nil || !available {
		return available, err
	}
	raw, available, err := e.call("eth_getTransactionReceipt", []any{txHash})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

func (e *EvmLowerBoundDetector) hasProof(height int64) (bool, error) {
	raw, available, err := e.call("eth_getProof", []any{evmZeroAddress, []string{}, evmBlockTag(height)})
	if err != nil || !available {
		return available, err
	}
	return !isEvmNullResult(raw), nil
}

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

func (e *EvmLowerBoundDetector) supportsStateOverride() bool {
	switch evmStateOverrideSupport(e.stateOverrideSupport.Load()) {
	case evmStateOverrideSupported:
		return true
	case evmStateOverrideUnsupported:
		return false
	}

	raw, available, err := e.call("eth_call", stateOverrideParams("latest"))
	if err == nil && available && !isEvmEmptyHexResult(raw) {
		e.stateOverrideSupport.Store(int32(evmStateOverrideSupported))
		return true
	}
	e.stateOverrideSupport.Store(int32(evmStateOverrideUnsupported))
	return false
}

func (e *EvmLowerBoundDetector) call(method string, params any) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, e.chain)
	if err != nil {
		return nil, false, err
	}
	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		respErr := response.GetError()
		if isEvmNoDataError(respErr) {
			return nil, false, nil
		}
		return nil, false, respErr
	}
	if isEvmNullResult(response.ResponseResult()) {
		return response.ResponseResult(), false, nil
	}
	return response.ResponseResult(), true, nil
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

func stateOverrideParams(block string) []any {
	return []any{
		map[string]any{
			"to":   stateCheckerAddress,
			"data": stateCheckerCallData,
		},
		block,
		map[string]any{
			stateCheckerAddress: map[string]any{
				"code": stateCheckerBytecode,
			},
		},
	}
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

func isEvmNoDataError(err *protocol.ResponseError) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Message)
	for _, hint := range evmNoDataErrorHints {
		if strings.Contains(message, hint) {
			return true
		}
	}
	return false
}

var evmNoDataErrorHints = []string{
	"no state available for block",
	"missing trie node",
	"header not found",
	"node state is pruned",
	"is not available, lowest height is",
	"state already discarded for",
	"your node is running with state pruning",
	"failed to compute tipset state",
	"bad tipset height",
	"body not found for block",
	"request beyond head block",
	"block not found",
	"could not find block",
	"unknown block",
	"header for hash not found",
	"after last accepted block",
	"version has either been pruned, or is for a future block",
	"no historical rpc is available for this historical",
	"historical backend error",
	"load state tree: failed to load state tree",
	"purged for block",
	"no state data",
	"state is not available",
	"block with such an id is pruned",
	"state at block",
	"unsupported block number",
	"unexpected state root",
	"evm module does not exist on height",
	"failed to load state at height",
	"no state found for block",
	"old data not available due",
	"state not found for block",
	"state does not maintain archive data",
	"access to archival, debug, or trace data is not included in your current plan",
	"empty reader set",
	"request might be querying historical state that is not available",
	"no receipts data",
	"no tx data",
	"no block data",
	"historical state not available in path scheme yet",
	"historical state is not available",
	"required historical state unavailable",
	"state histories haven't been fully indexed yet",
	"but it out-of-bounds",
	"has been pruned; earliest available is",
	"error loading messages for tipset",
	"height is not available",
	"method handler crashed",
	"unexpected error",
	"invalid block height",
	"pruned history unavailable",
	"no transactions snapshot file for",
	"could not find block for height",
	"transaction indexing is in progress",
	"old data not available due to pruning",
	"requested epoch was a null round",
}

var _ lower_bounds.LowerBoundDetector = (*EvmLowerBoundDetector)(nil)
