package ton_validations

import (
	"strconv"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type TonV3ChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewTonV3ChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *TonV3ChainValidator {
	return &TonV3ChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonV3ChainValidator) Validate() validations.ValidationSettingResult {
	info, err := FetchV3MasterchainInfo(t.connector, t.chain.Chain, t.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch ton v3 masterchain info for upstream '%s'", t.upstreamId)
		return validations.SettingsError
	}
	// global_id is present in every TON block header and is never 0 on a
	// real network (-239 mainnet, -3 testnet); 0 means the field is missing
	if info.Last.GlobalId == 0 {
		log.Error().Msgf("ton v3 upstream '%s' reported no global_id in the last masterchain block", t.upstreamId)
		return validations.SettingsError
	}
	// the registry stores TON chain ids as decimal strings ("-239"/"-3")
	if strconv.FormatInt(info.Last.GlobalId, 10) == t.chain.ChainId {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects chain id '%s' but ton v3 upstream '%s' reports global_id '%d'",
		t.chain.Chain.String(),
		t.chain.ChainId,
		t.upstreamId,
		info.Last.GlobalId,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*TonV3ChainValidator)(nil)
