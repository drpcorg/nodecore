package aptos_bounds

import (
	"context"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/internal/upstreams/validations/aptos_validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

const aptosPeriod = 5 * time.Minute

type AptosLowerBoundDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewAptosLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *AptosLowerBoundDetector {
	return &AptosLowerBoundDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

// DetectLowerBound reads the oldest retained state version and block height
// straight from GET /v1 - Aptos exposes both directly, so no binary search is
// needed (unlike Algorand).
func (a *AptosLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	info, err := a.fetchLedgerInfo()
	if err != nil {
		log.Warn().Err(err).Msgf("aptos upstream '%s' lower-bound fetch failed; emitting UnknownBound", a.upstreamId)
		return []protocol.LowerBoundData{protocol.NewLowerBoundDataNow(0, protocol.UnknownBound)}, nil
	}
	state, _ := aptos_validations.ParseU64(info.OldestLedgerVersion)
	block, _ := aptos_validations.ParseU64(info.OldestBlockHeight)
	return []protocol.LowerBoundData{
		protocol.NewLowerBoundDataNow(int64(state), protocol.StateBound),
		protocol.NewLowerBoundDataNow(int64(block), protocol.BlockBound),
	}, nil
}

func (a *AptosLowerBoundDetector) SupportedTypes() []protocol.LowerBoundType {
	return []protocol.LowerBoundType{protocol.StateBound, protocol.BlockBound, protocol.UnknownBound}
}

func (a *AptosLowerBoundDetector) Period() time.Duration {
	return aptosPeriod
}

func (a *AptosLowerBoundDetector) fetchLedgerInfo() (*aptos_validations.AptosLedgerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/v1", nil, a.chain)
	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var info aptos_validations.AptosLedgerInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

var _ lower_bounds.LowerBoundDetector = (*AptosLowerBoundDetector)(nil)
