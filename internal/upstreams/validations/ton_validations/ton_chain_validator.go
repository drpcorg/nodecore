package ton_validations

import (
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type tonZerostate struct {
	rootHash string
	fileHash string
}

// base64 hashes of the network zerostate (result.init of getMasterchainInfo),
// verified live against production mainnet and public testnet.toncenter.com nodes
var expectedZerostates = map[chains.Chain]tonZerostate{
	chains.TON: {
		rootHash: "F6OpKZKqvqeFp6CQmFomXNMfMj2EnaUSOXN+Mh+wVWk=",
		fileHash: "XplPz01CXAps5qeSWUtxcyBfdAo5zVb1N979KLSKD24=",
	},
	chains.TON_TESTNET: {
		rootHash: "gj+B8wb/AmlPk1z1AhVI484rhrUpgSr2oSFIh56VoSg=",
		fileHash: "Z+IKwYS54DmmJmesw/nAD5DzWadnOCMzee+kdgSYDOg=",
	},
}

type TonChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewTonChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *TonChainValidator {
	return &TonChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonChainValidator) Validate() validations.ValidationSettingResult {
	expected, hasMapping := expectedZerostates[t.chain.Chain]
	if !hasMapping {
		// without a zerostate we can't tell what network the upstream is on
		log.Error().Msgf(
			"no expected zerostate for chain '%s', can't validate the chain of ton upstream '%s'",
			t.chain.Chain.String(),
			t.upstreamId,
		)
		return validations.FatalSettingError
	}
	info, err := fetchMasterchainInfo(t.connector, t.chain.Chain, t.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch ton masterchain info for upstream '%s'", t.upstreamId)
		return validations.SettingsError
	}
	// base64 is case-sensitive, exact match only
	if info.Result.Init.RootHash == expected.rootHash && info.Result.Init.FileHash == expected.fileHash {
		return validations.Valid
	}
	log.Error().Msgf(
		"'%s' expects zerostate root_hash '%s' file_hash '%s' but ton upstream '%s' reports root_hash '%s' file_hash '%s'",
		t.chain.Chain.String(),
		expected.rootHash,
		expected.fileHash,
		t.upstreamId,
		info.Result.Init.RootHash,
		info.Result.Init.FileHash,
	)
	return validations.FatalSettingError
}

var _ validations.SettingsValidator = (*TonChainValidator)(nil)
