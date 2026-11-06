package eth_validations

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const (
	erigonBugCheckFrequency = 10
	erigonBugMaxSearchDepth = 20
	erigonBugProbeAddress   = "0x1111111111111111111111111111111111111111"
	erigonBugProbeCallData  = "0x1eaf190c"
	// a contract whose only function (selector 0x1eaf190c) returns its own balance; installed via
	// state override so that a correct node answers with 32 bytes of data at any block with state
	erigonBugProbeBytecode = "0x6080604052348015600e575f5ffd5b50600436106026575f3560e01c80631eaf190c14602a575b5f5ffd5b60306044565b604051603b91906078565b60405180910390f35b5f5f73ffffffffffffffffffffffffffffffffffffffff1631905090565b5f819050919050565b6072816062565b82525050565b5f60208201905060895f830184606b565b9291505056fea2646970667358221220251f5b4d2ed1abe77f66fde198a57ada08562dc3b0afbc6bac0261d1bf516b5d64736f6c634300081e0033"
)

// EthErigonBugValidator detects Erigon builds that answer a state-override eth_call at historical
// blocks with an empty result ("0x") instead of the contract's return data. Such an upstream serves
// wrong historical state, so the bug is a fatal settings error until a later check is clean.
type EthErigonBugValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	callCount       atomic.Int64
	lastResult      atomic.Int32
}

func NewEthErigonBugValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthErigonBugValidator {
	timeout := time.Second
	if options != nil && options.InternalTimeout > 0 {
		timeout = options.InternalTimeout
	}
	return &EthErigonBugValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: timeout,
	}
}

func (v *EthErigonBugValidator) Validate() validations.ValidationSettingResult {
	currentCount := v.callCount.Add(1)
	if (currentCount-1)%erigonBugCheckFrequency != 0 {
		return validations.ValidationSettingResult(v.lastResult.Load())
	}

	result := v.validateNow()
	v.lastResult.Store(int32(result))
	return result
}

func (v *EthErigonBugValidator) validateNow() validations.ValidationSettingResult {
	isErigon, err := v.isErigon()
	if err != nil {
		log.Warn().Err(err).Msgf("failed to read the client version for the Erigon bug validation of upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}
	if !isErigon {
		return validations.Valid
	}

	latest, err := v.getLatestBlockNumber()
	if err != nil || latest == 0 {
		if err != nil {
			log.Warn().Err(err).Msgf("failed to read the latest block for the Erigon bug validation of upstream '%s'", v.upstreamId)
		}
		return validations.SettingsError
	}

	return v.searchForBug(latest)
}

func (v *EthErigonBugValidator) isErigon() (bool, error) {
	version, err := v.getStringResult("web3_clientVersion", nil)
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(version), "erigon"), nil
}

func (v *EthErigonBugValidator) getLatestBlockNumber() (uint64, error) {
	raw, err := v.getStringResult("eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}
	return protocol.ParseHexUint64(strings.TrimSpace(raw))
}

// searchForBug binary-searches [1, latest] for a block where the probe answers "0x": blocks with
// data move the search towards older blocks, errors (no state, timeouts) towards newer ones.
// A search that never saw data is inconclusive rather than valid.
func (v *EthErigonBugValidator) searchForBug(latest uint64) validations.ValidationSettingResult {
	result := validations.SettingsError
	start, end := uint64(1), latest
	for depth := 0; depth <= erigonBugMaxSearchDepth && start < end; depth++ {
		mid := start + (end-start)/2
		switch v.probeBlock(mid) {
		case validations.FatalSettingError:
			log.Error().Msgf("Erigon balance bug detected on upstream '%s' at block %d", v.upstreamId, mid)
			return validations.FatalSettingError
		case validations.Valid:
			result = validations.Valid
			end = mid
		default:
			start = mid + 1
		}
	}
	return result
}

// probeBlock answers FatalSettingError for the bug ("0x"), Valid for return data and SettingsError
// for anything else (no state at that block, timeout, transport or JSON-RPC error).
func (v *EthErigonBugValidator) probeBlock(height uint64) validations.ValidationSettingResult {
	params := []any{
		map[string]any{
			"to":   erigonBugProbeAddress,
			"data": erigonBugProbeCallData,
		},
		"0x" + strconv.FormatUint(height, 16),
		map[string]any{
			erigonBugProbeAddress: map[string]any{
				"code": erigonBugProbeBytecode,
			},
		},
	}
	result, err := v.getStringResult("eth_call", params)
	if err != nil {
		return validations.SettingsError
	}
	switch {
	case result == "0x":
		return validations.FatalSettingError
	case strings.HasPrefix(result, "0x"):
		return validations.Valid
	default:
		return validations.SettingsError
	}
}

func (v *EthErigonBugValidator) getStringResult(method string, params any) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest(method, params, v.chain.Chain)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), v.internalTimeout)
	defer cancel()
	response := v.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	return response.ResponseResultString()
}

var _ validations.SettingsValidator = (*EthErigonBugValidator)(nil)
