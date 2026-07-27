package bitcoin_validations

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var (
	errBitcoinInitialBlockDownload = errors.New("bitcoin node is in initial block download")
	errBitcoinNoPeers              = errors.New("bitcoin node has no peers")
)

type BitcoinHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	validatePeers   bool
}

func NewBitcoinHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
	validatePeers bool,
) *BitcoinHealthValidator {
	return &BitcoinHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
		validatePeers:   validatePeers,
	}
}

func (b *BitcoinHealthValidator) Validate() protocol.AvailabilityStatus {
	info, err := b.fetchBlockchainInfo()
	if err != nil {
		log.Error().Err(err).Msgf("bitcoin upstream '%s' health validation failed", b.upstreamId)
		return protocol.Unavailable
	}
	if info.InitialBlockDownload {
		log.Warn().Err(errBitcoinInitialBlockDownload).Msgf("bitcoin upstream '%s' is in initial block download", b.upstreamId)
		return protocol.Syncing
	}
	syncingLag := b.chain.Settings.Lags.Syncing
	if syncingLag > 0 && info.Headers >= info.Blocks && int64(info.Headers-info.Blocks) > syncingLag {
		log.Warn().Msgf(
			"bitcoin upstream '%s' is syncing, headers=%d blocks=%d",
			b.upstreamId,
			info.Headers,
			info.Blocks,
		)
		return protocol.Syncing
	}
	if b.validatePeers {
		peers, err := b.fetchConnectionCount()
		if err != nil {
			log.Error().Err(err).Msgf("failed to fetch bitcoin connection count for upstream '%s'", b.upstreamId)
			return protocol.Unavailable
		}
		if peers == 0 {
			log.Error().Err(errBitcoinNoPeers).Msgf("bitcoin upstream '%s' has no peers", b.upstreamId)
			return protocol.Unavailable
		}
	}
	return protocol.Available
}

func (b *BitcoinHealthValidator) fetchBlockchainInfo() (*BitcoinBlockchainInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getblockchaininfo", []any{}, b.chain.Chain)
	if err != nil {
		return nil, err
	}
	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	var info BitcoinBlockchainInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (b *BitcoinHealthValidator) fetchConnectionCount() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getconnectioncount", []any{}, b.chain.Chain)
	if err != nil {
		return 0, err
	}
	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	return strconv.ParseUint(protocol.ResultAsString(response.ResponseResult()), 10, 64)
}

type BitcoinBlockchainInfo struct {
	Blocks               uint64 `json:"blocks"`
	Headers              uint64 `json:"headers"`
	InitialBlockDownload bool   `json:"initialblockdownload"`
	Pruned               bool   `json:"pruned"`
	PruneHeight          uint64 `json:"pruneheight"`
}

var _ validations.HealthValidator = (*BitcoinHealthValidator)(nil)
