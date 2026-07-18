package bitcoin_validations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errBitcoinEmptyGenesis = errors.New("bitcoin node returned empty genesis hash")

var expectedGenesisHashes = map[chains.Chain]string{
	chains.BITCOIN:          "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f",
	chains.BITCOIN_TESTNET:  "000000000933ea01ad0ee984209779baaec3ced90fa3f408719526f8d77f4943",
	chains.DOGECOIN:         "1a91e3dace36e2be3bf030a65679fe821aa1d6ef92e7c9902eb318182c355691",
	chains.DOGECOIN_TESTNET: "bb0a78264637406b6360aad926284d544d7049f45189db5664f3c4d07350559e",
}

type BitcoinChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewBitcoinChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *BitcoinChainValidator {
	return &BitcoinChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (b *BitcoinChainValidator) Validate() validations.ValidationSettingResult {
	expected, hasMapping := expectedGenesisHashes[b.chain.Chain]
	if !hasMapping {
		log.Warn().Msgf(
			"no expected genesis hash for chain '%s', skipping chain validation of bitcoin upstream '%s'",
			b.chain.Chain.String(),
			b.upstreamId,
		)
		return validations.Valid
	}
	genesis, err := b.fetchGenesisHash()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch bitcoin genesis hash for upstream '%s'", b.upstreamId)
		return validations.SettingsError
	}
	if strings.EqualFold(genesis, expected) {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects genesis hash '%s' but bitcoin upstream '%s' reports '%s'",
		b.chain.Chain.String(),
		expected,
		b.upstreamId,
		genesis,
	)
	return validations.FatalSettingError
}

func (b *BitcoinChainValidator) fetchGenesisHash() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getblockhash", []any{0}, b.chain.Chain)
	if err != nil {
		return "", err
	}
	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	genesis := protocol.ResultAsString(response.ResponseResult())
	if genesis == "" {
		return "", errBitcoinEmptyGenesis
	}
	return genesis, nil
}

var _ validations.SettingsValidator = (*BitcoinChainValidator)(nil)
