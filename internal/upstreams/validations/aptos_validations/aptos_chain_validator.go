package aptos_validations

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type AptosChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewAptosChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *AptosChainValidator {
	return &AptosChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AptosChainValidator) Validate() validations.ValidationSettingResult {
	expected := strings.TrimSpace(a.chain.ChainId)
	if expected == "" {
		return validations.Valid
	}
	expectedId, err := parseHexChainId(expected)
	if err != nil {
		log.Error().Err(err).Msgf("aptos upstream '%s' has unparseable chain-id '%s'", a.upstreamId, expected)
		return validations.FatalSettingError
	}

	info, err := a.fetchLedgerInfo()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch aptos ledger info for upstream '%s'", a.upstreamId)
		return validations.SettingsError
	}
	// A body without chain_id (e.g. a gateway's 200 error envelope) is a
	// transient fetch problem, not proof of a wrong chain: retry, don't kill
	// the upstream. No real Aptos network uses chain_id 0.
	if info.ChainId == 0 {
		log.Warn().Msgf("aptos upstream '%s' returned ledger info without chain_id; will retry validation", a.upstreamId)
		return validations.SettingsError
	}
	if info.ChainId == expectedId {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' (expected node chain_id=%d) but aptos upstream '%s' reports chain_id=%d",
		a.chain.Chain.String(), expected, expectedId, a.upstreamId, info.ChainId,
	)
	return validations.FatalSettingError
}

// parseHexChainId converts the registry's "0x1" / "0x2" sentinel into the
// numeric chain_id the Aptos node reports (1 / 2).
func parseHexChainId(chainId string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(strings.ToLower(chainId), "0x"), 16, 64)
}

func (a *AptosChainValidator) fetchLedgerInfo() (*AptosLedgerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	return FetchLedgerInfo(ctx, a.connector, a.chain.Chain)
}

var _ validations.SettingsValidator = (*AptosChainValidator)(nil)
