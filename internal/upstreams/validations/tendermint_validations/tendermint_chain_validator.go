package tendermint_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type TendermintChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewTendermintChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *TendermintChainValidator {
	return &TendermintChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TendermintChainValidator) Validate() validations.ValidationSettingResult {
	expected := strings.TrimSpace(t.chain.ChainId)
	if expected == "" {
		return validations.Valid
	}

	ctx, cancel := context.WithTimeout(context.Background(), t.internalTimeout)
	defer cancel()

	status, err := specific_helpers.FetchTendermintStatus(ctx, t.connector, t.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the tendermint status for upstream '%s'", t.upstreamId)
		return validations.SettingsError
	}
	if strings.EqualFold(status.NodeInfo.Network, expected) {
		return validations.Valid
	}

	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' but tendermint upstream '%s' reports network '%s'",
		t.chain.Chain.String(), expected, t.upstreamId, status.NodeInfo.Network,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*TendermintChainValidator)(nil)
