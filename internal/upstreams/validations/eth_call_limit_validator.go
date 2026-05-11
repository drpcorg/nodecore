package validations

import (
	"context"
	"fmt"
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
)

var IgnoredCallLimitErrors = []string{"rpc.returndata.limit"}

type EthCallLimitValidator struct {
	upstreamId string
	connector  connectors.ApiConnector
	chain      *chains.ConfiguredChain
	option     *chains.Options

	executor failsafe.Executor[protocol.ResponseHolder]
}

func (e *EthCallLimitValidator) Validate() ValidationSettingResult {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest(
		"eth_call",
		[]any{
			map[string]any{
				"to": e.chain.CallValidateContract,
				// 4-byte selector (0xd8a26e3a) + 32-byte uint256 size, ABI-encoded
				"data": fmt.Sprintf("0xd8a26e3a%064x", e.option.CallLimitSize),
			},
			"latest",
		},
		e.chain.Chain,
	)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request to detect call limit of upstream '%s'", e.upstreamId)
		return SettingsError
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.option.InternalTimeout)
	defer cancel()

	response, _ := e.executor.Get(func() (protocol.ResponseHolder, error) {
		return e.connector.SendRequest(ctx, req), nil
	})
	if response.HasError() {
		if strings.Contains(response.GetError().Message, "rpc.returndata.limit") {
			log.Error().Err(response.GetError()).
				Msgf(
					"'%s' upstream is probably incorrectly configured. "+
						"You need to set up your return limit to at least %d. "+
						"Erigon config example: https://github.com/ledgerwatch/erigon/blob/d014da4dc039ea97caf04ed29feb2af92b7b129d/cmd/utils/flags.go#L369",
					e.upstreamId, e.option.CallLimitSize,
				)
			return FatalSettingError
		}
		return SettingsError
	}

	return Valid
}

func NewEthCallLimitValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthCallLimitValidator {
	return &EthCallLimitValidator{
		upstreamId: upstreamId,
		connector:  connector,
		chain:      chain,
		option:     options,
		executor:   validatorExecutor(upstreamId, "ethCallLimitValidation", IgnoredCallLimitErrors),
	}
}

var _ SettingsValidator = (*EthCallLimitValidator)(nil)
