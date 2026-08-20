package sui_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/specific_helpers"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// SuiChainValidator compares GetServiceInfoResponse.chain_id (the genesis
// checkpoint digest) with the configured chain-id - the EVM chain-id
// validation analog, and stricter than matching the human-readable network
// name. chains.yaml holds base58 digests and the registry loader lowercases
// every chain-id, so the compare is case-insensitive by necessity.
type SuiChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewSuiChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *SuiChainValidator {
	return &SuiChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *SuiChainValidator) Validate() validations.ValidationSettingResult {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	serviceInfo, _, err := specific_helpers.FetchSuiServiceInfo(ctx, s.connector, s.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch the sui service info for upstream '%s'", s.upstreamId)
		return validations.SettingsError
	}
	// an empty chain_id fails this compare too - the configured chain-id is
	// never empty here, since SettingsValidators() skips the validator entirely
	// in that case
	if strings.EqualFold(serviceInfo.GetChainId(), s.chain.ChainId) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects chain id '%s' but sui upstream '%s' reports '%s' (chain '%s')",
		s.chain.Chain.String(),
		s.chain.ChainId,
		s.upstreamId,
		serviceInfo.GetChainId(),
		serviceInfo.GetChain(),
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*SuiChainValidator)(nil)

// SuiHealthValidator issues GetServiceInfo: a transport/status error means
// Unavailable, otherwise Available. Sui exposes no syncing flag;
// timestamp-lag-based Syncing detection is a possible later refinement.
type SuiHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewSuiHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *SuiHealthValidator {
	return &SuiHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *SuiHealthValidator) Validate() protocol.AvailabilityStatus {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	if _, _, err := specific_helpers.FetchSuiServiceInfo(ctx, s.connector, s.chain.Chain); err != nil {
		log.Warn().Err(err).Msgf("sui upstream '%s' health validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

var _ validations.Validator[protocol.AvailabilityStatus] = (*SuiHealthValidator)(nil)
