package eth_validations

import (
	"context"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/gas_limit"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// EthGasLimitValidator checks that the node grants the extra eth_call gas budget. A node that
// grants it is valid, a node that reports a lower budget is a fatal settings error, and a node
// that gives no usable reading (an error, an empty result, a value that is not a gas reading) is
// a non-fatal settings error: there is nothing to judge it by, so its state is left as it is.
type EthGasLimitValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewEthGasLimitValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthGasLimitValidator {
	return &EthGasLimitValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: options.InternalTimeout,
	}
}

// Validate sends a single request and never retries: a failed call is a non-fatal settings
// error and the next validation asks again.
func (v *EthGasLimitValidator) Validate(ctx context.Context) validations.ValidationSettingResult {
	granted, err := gas_limit.Measure(ctx, v.connector, v.chain.Chain, v.internalTimeout)
	if err != nil {
		log.Warn().Err(err).Msgf("unable to measure the eth_call gas limit of upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}
	if gas_limit.GrantsExtraGas(granted) {
		return validations.Valid
	}

	log.Error().Msgf(
		"'%s' upstream is probably incorrectly configured. Its node grants %s gas to eth_call, "+
			"you need to raise the node's RPC gas cap to at least 600000000",
		v.upstreamId, granted,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*EthGasLimitValidator)(nil)
