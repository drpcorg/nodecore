package eth_validations

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

type gasPriceRule struct {
	op    string
	value uint64
}

type EthGasPriceValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	rules           []gasPriceRule
	executor        failsafe.Executor[protocol.ResponseHolder]
}

func NewEthGasPriceValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthGasPriceValidator {
	timeout := time.Second
	if options != nil && options.InternalTimeout > 0 {
		timeout = options.InternalTimeout
	}
	return &EthGasPriceValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: timeout,
		rules:           parseGasPriceRules(chain),
		executor:        validations.ValidatorExecutor(upstreamId, "ethGasPriceValidation", nil),
	}
}

func (v *EthGasPriceValidator) Validate() validations.ValidationSettingResult {
	if len(v.rules) == 0 {
		return validations.Valid
	}

	ctx, cancel := context.WithTimeout(context.Background(), v.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_gasPrice", nil, v.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to build eth_gasPrice validation request for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	response, _ := v.executor.Get(func() (protocol.ResponseHolder, error) {
		return v.connector.SendRequest(ctx, request), nil
	})
	if response.HasError() {
		log.Warn().Err(response.GetError()).Msgf("failed to read gas price for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	gasPriceRaw, err := response.ResponseResultString()
	if err != nil {
		log.Warn().Err(err).Msgf("failed to read gas price result for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}
	gasPrice, err := parseHexUint64(strings.TrimSpace(gasPriceRaw))
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse gas price for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	for _, rule := range v.rules {
		if !rule.matches(gasPrice) {
			log.Warn().Msgf("upstream '%s' has gasPrice %d, rule '%s %d' failed", v.upstreamId, gasPrice, rule.op, rule.value)
			return validations.FatalSettingError
		}
	}

	return validations.Valid
}

func parseGasPriceRules(chain *chains.ConfiguredChain) []gasPriceRule {
	if chain == nil || len(chain.GasPriceCondition) == 0 {
		return nil
	}
	rules := make([]gasPriceRule, 0, len(chain.GasPriceCondition))
	for _, raw := range chain.GasPriceCondition {
		rule, err := parseGasPriceRule(raw)
		if err != nil {
			log.Warn().Err(err).Msgf("ignoring invalid gas-price-condition '%s' for chain '%s'", raw, chain.Chain.String())
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func parseGasPriceRule(raw string) (gasPriceRule, error) {
	parts := strings.Fields(raw)
	if len(parts) != 2 {
		return gasPriceRule{}, fmt.Errorf("expected '<op> <value>'")
	}
	value, err := strconv.ParseUint(parts[1], 0, 64)
	if err != nil {
		return gasPriceRule{}, err
	}
	switch parts[0] {
	case "eq", "ne", "gt", "gte", "lt", "lte":
		return gasPriceRule{op: parts[0], value: value}, nil
	default:
		return gasPriceRule{}, fmt.Errorf("unsupported operator '%s'", parts[0])
	}
}

func (r gasPriceRule) matches(actual uint64) bool {
	switch r.op {
	case "eq":
		return actual == r.value
	case "ne":
		return actual != r.value
	case "gt":
		return actual > r.value
	case "gte":
		return actual >= r.value
	case "lt":
		return actual < r.value
	case "lte":
		return actual <= r.value
	default:
		return true
	}
}

func parseHexUint64(raw string) (uint64, error) {
	trimmed := strings.Trim(raw, "\"")
	return strconv.ParseUint(trimmed, 0, 64)
}
