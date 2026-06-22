package eth_validations

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

const (
	logIndexCheckFrequency         = 10
	logIndexCheckTxCount           = 6
	logIndexMaxBlockSearchAttempts = 5
)

type EthLogIndexValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	executor        failsafe.Executor[protocol.ResponseHolder]
	callCount       atomic.Int64
	lastResult      atomic.Int32
}

type logIndexBlock struct {
	Transactions []logIndexTx `json:"transactions"`
}

type logIndexTx struct {
	Hash string `json:"hash"`
}

type logIndexReceipt struct {
	Logs []logIndexLog `json:"logs"`
}

type logIndexLog struct {
	LogIndex string `json:"logIndex"`
}

type logIndexTxData struct {
	hash    string
	receipt logIndexReceipt
}

func NewEthLogIndexValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthLogIndexValidator {
	timeout := time.Second
	if options != nil && options.InternalTimeout > 0 {
		timeout = options.InternalTimeout
	}
	validator := &EthLogIndexValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: timeout,
		executor:        validations.ValidatorExecutor(upstreamId, "ethLogIndexValidation", nil),
	}
	validator.lastResult.Store(int32(validations.Valid))
	return validator
}

func (v *EthLogIndexValidator) Validate() validations.ValidationSettingResult {
	currentCount := v.callCount.Add(1)
	if (currentCount-1)%logIndexCheckFrequency != 0 {
		return validations.ValidationSettingResult(v.lastResult.Load())
	}

	result := v.validateNow()
	v.lastResult.Store(int32(result))
	return result
}

func (v *EthLogIndexValidator) validateNow() validations.ValidationSettingResult {
	latest, err := v.getLatestBlockNumber()
	if err != nil || latest == 0 {
		if err != nil {
			log.Warn().Err(err).Msgf("failed to read latest block for logIndex validation of upstream '%s'", v.upstreamId)
		}
		return validations.ValidationSettingResult(v.lastResult.Load())
	}

	for attempt := uint64(0); attempt < logIndexMaxBlockSearchAttempts && latest > attempt; attempt++ {
		blockHex := "0x" + strconv.FormatUint(latest-attempt, 16)
		result, ok := v.validateBlock(blockHex)
		if ok {
			return result
		}
	}

	return validations.ValidationSettingResult(v.lastResult.Load())
}

func (v *EthLogIndexValidator) validateBlock(blockHex string) (validations.ValidationSettingResult, bool) {
	block, err := v.getBlock(blockHex)
	if err != nil || len(block.Transactions) < 2 {
		return validations.Valid, false
	}

	found := make([]logIndexTxData, 0, 2)
	maxSearchCount := min(logIndexCheckTxCount, len(block.Transactions))
	for i := 0; i < maxSearchCount && len(found) < 2; i++ {
		txHash := block.Transactions[i].Hash
		if txHash == "" {
			log.Warn().Msgf("transaction at index %d has no hash during logIndex validation of upstream '%s'", i, v.upstreamId)
			return validations.ValidationSettingResult(v.lastResult.Load()), true
		}
		receipt, err := v.getTransactionReceipt(txHash)
		if err != nil {
			log.Warn().Err(err).Msgf("failed to read receipt '%s' during logIndex validation of upstream '%s'", txHash, v.upstreamId)
			return validations.ValidationSettingResult(v.lastResult.Load()), true
		}
		if len(receipt.Logs) > 0 {
			found = append(found, logIndexTxData{hash: txHash, receipt: receipt})
		}
	}

	if len(found) < 2 {
		return validations.ValidationSettingResult(v.lastResult.Load()), true
	}
	return v.validateLogIndices(found[0], found[1]), true
}

func (v *EthLogIndexValidator) validateLogIndices(first, second logIndexTxData) validations.ValidationSettingResult {
	firstFirst, ok := parseLogIndexValue(first.receipt.Logs[0].LogIndex)
	if !ok {
		return validations.ValidationSettingResult(v.lastResult.Load())
	}
	firstLast, ok := parseLogIndexValue(first.receipt.Logs[len(first.receipt.Logs)-1].LogIndex)
	if !ok {
		return validations.ValidationSettingResult(v.lastResult.Load())
	}
	secondFirst, ok := parseLogIndexValue(second.receipt.Logs[0].LogIndex)
	if !ok {
		return validations.ValidationSettingResult(v.lastResult.Load())
	}

	if firstFirst != 0 {
		log.Error().Msgf("upstream '%s' has incorrect logIndex start in transaction '%s': first log index is %d instead of 0", v.upstreamId, first.hash, firstFirst)
		return validations.FatalSettingError
	}
	expectedSecondStart := firstLast + 1
	if secondFirst == 0 || secondFirst != expectedSecondStart {
		log.Error().Msgf("upstream '%s' has non-global logIndex: tx '%s' last index %d, tx '%s' first index %d, expected %d", v.upstreamId, first.hash, firstLast, second.hash, secondFirst, expectedSecondStart)
		return validations.FatalSettingError
	}
	return validations.Valid
}

func (v *EthLogIndexValidator) getLatestBlockNumber() (uint64, error) {
	response, err := v.sendJsonRpc("eth_blockNumber", nil)
	if err != nil || response.HasError() {
		if err != nil {
			return 0, err
		}
		return 0, response.GetError()
	}
	raw, err := response.ResponseResultString()
	if err != nil {
		return 0, err
	}
	return parseHexUint64(strings.TrimSpace(raw))
}

func (v *EthLogIndexValidator) getBlock(blockHex string) (logIndexBlock, error) {
	response, err := v.sendJsonRpc("eth_getBlockByNumber", []any{blockHex, true})
	if err != nil || response.HasError() {
		if err != nil {
			return logIndexBlock{}, err
		}
		return logIndexBlock{}, response.GetError()
	}
	var block logIndexBlock
	if err := sonic.Unmarshal(response.ResponseResult(), &block); err != nil {
		return logIndexBlock{}, err
	}
	return block, nil
}

func (v *EthLogIndexValidator) getTransactionReceipt(txHash string) (logIndexReceipt, error) {
	response, err := v.sendJsonRpc("eth_getTransactionReceipt", []any{txHash})
	if err != nil || response.HasError() {
		if err != nil {
			return logIndexReceipt{}, err
		}
		return logIndexReceipt{}, response.GetError()
	}
	var receipt logIndexReceipt
	if err := sonic.Unmarshal(response.ResponseResult(), &receipt); err != nil {
		return logIndexReceipt{}, err
	}
	return receipt, nil
}

func (v *EthLogIndexValidator) sendJsonRpc(method string, params any) (protocol.ResponseHolder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), v.internalTimeout)
	defer cancel()
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, v.chain.Chain)
	if err != nil {
		return nil, err
	}
	return v.executor.Get(func() (protocol.ResponseHolder, error) {
		return v.connector.SendRequest(ctx, request), nil
	})
}

func parseLogIndexValue(raw string) (uint64, bool) {
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	value, err := parseHexUint64(raw)
	return value, err == nil
}

var _ validations.SettingsValidator = (*EthLogIndexValidator)(nil)
