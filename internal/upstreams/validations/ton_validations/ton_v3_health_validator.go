package ton_validations

import (
	"errors"
	"strconv"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// The indexer commits masterchain blocks in batches, so its head can sit a
// few dozen seconds behind real time even when perfectly healthy. TON's
// ExpectedBlockTime * Lags.Syncing is 1s * 40 = 40s - too tight for that
// commit cadence - so the freshness threshold gets a floor of one minute.
const tonV3MinFreshness = time.Minute

var errTonV3NoSeqno = errors.New("ton v3 indexer returned zero masterchain seqno")

type TonV3HealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewTonV3HealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *TonV3HealthValidator {
	return &TonV3HealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TonV3HealthValidator) Validate() protocol.AvailabilityStatus {
	info, err := FetchV3MasterchainInfo(t.connector, t.chain.Chain, t.internalTimeout)
	if err != nil {
		log.Error().Err(err).Msgf("ton v3 upstream '%s' health validation failed", t.upstreamId)
		return protocol.Unavailable
	}
	if info.Last.Seqno == 0 {
		log.Error().Err(errTonV3NoSeqno).Msgf("ton v3 upstream '%s' has no masterchain head", t.upstreamId)
		return protocol.Unavailable
	}
	// v3 serves gen_utime as a string of unix seconds; if it is missing or
	// unparseable, skip the freshness guard rather than flap the upstream
	genUtime, err := strconv.ParseInt(info.Last.GenUtime, 10, 64)
	if err != nil {
		log.Warn().Msgf(
			"ton v3 upstream '%s' reported unparseable gen_utime %q, skipping the freshness check",
			t.upstreamId, info.Last.GenUtime,
		)
		return protocol.Available
	}
	threshold := t.chain.Settings.ExpectedBlockTime * time.Duration(t.chain.Settings.Lags.Syncing)
	if threshold <= 0 {
		return protocol.Available
	}
	if threshold < tonV3MinFreshness {
		threshold = tonV3MinFreshness
	}
	if age := time.Since(time.Unix(genUtime, 0)); age > threshold {
		log.Warn().Msgf(
			"ton v3 upstream '%s' head is stale: gen_utime is %s old (threshold %s)",
			t.upstreamId, age, threshold,
		)
		return protocol.Syncing
	}
	return protocol.Available
}

var _ validations.HealthValidator = (*TonV3HealthValidator)(nil)
