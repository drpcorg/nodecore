package validations

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

type EthPeersValidator struct {
	upstreamId string
	chain      chains.Chain
	connector  connectors.ApiConnector
	options    *chains.Options
}

func (e *EthPeersValidator) Validate() protocol.AvailabilityStatus {
	peerCountResp, err := e.getPeerCount()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get peer count of upstream '%s'", e.upstreamId)
		return protocol.Unavailable
	}
	var raw string
	if err := sonic.Unmarshal(peerCountResp, &raw); err != nil {
		log.Error().
			Err(err).
			Msgf("unable to unmarshal peer count of upstream '%s', response - %s", e.upstreamId, string(peerCountResp))
		return protocol.Unavailable
	}

	peers, err := strconv.ParseInt(raw, 0, 64)
	if err != nil {
		log.Error().Err(err).Msgf("unable to parse peer count to int of upstream '%s', raw - %s", e.upstreamId, raw)
		return protocol.Unavailable
	}
	if peers < e.options.MinPeers {
		return protocol.Immature
	}
	return protocol.Available
}

func NewEthPeersValidator(
	upstreamId string,
	chain chains.Chain,
	connector connectors.ApiConnector,
	options *chains.Options,
) *EthPeersValidator {
	return &EthPeersValidator{
		upstreamId: upstreamId,
		chain:      chain,
		connector:  connector,
		options:    options,
	}
}

func (e *EthPeersValidator) getPeerCount() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.options.InternalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("net_peerCount", nil, e.chain)
	if err != nil {
		return nil, err
	}

	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}

	return response.ResponseResult(), nil
}

type EthSyncingValidator struct {
	upstreamId      string
	chain           *chains.ConfiguredChain
	connector       connectors.ApiConnector
	internalTimeout time.Duration
}

func (e *EthSyncingValidator) Validate() protocol.AvailabilityStatus {
	syncingResp, err := e.getSyncingStatus()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get syncing status of upstream '%s'", e.upstreamId)
		return protocol.Unavailable
	}

	root, err := sonic.Get(syncingResp)
	if err != nil {
		log.Error().Err(err).Msgf("unable to get syncing status as a node of upstream '%s'", e.upstreamId)
		return protocol.Unavailable
	}
	if b, err := root.Bool(); err == nil {
		if b {
			return protocol.Syncing
		}
		return protocol.Available
	}

	if cur, high := root.Get("currentBlock"), root.Get("highestBlock"); cur.Exists() && high.Exists() {
		curStr, _ := cur.String()
		highStr, _ := high.String()

		current, ok1 := parseHexBig(curStr)
		highest, ok2 := parseHexBig(highStr)
		if ok1 && ok2 {
			lag := new(big.Int).Sub(highest, current)
			if lag.Cmp(big.NewInt(e.chain.Settings.Lags.Syncing)) > 0 {
				return protocol.Syncing
			}
			return protocol.Available
		}
	}
	if root.Get("batchProcessed").Exists() &&
		root.Get("batchSeen").Exists() &&
		root.Get("syncTargetMsgCount").Exists() {
		return protocol.Syncing
	}
	return protocol.Available
}

func NewEthSyncingValidator(
	upstreamId string,
	chain *chains.ConfiguredChain,
	connector connectors.ApiConnector,
	internalTimeout time.Duration,
) *EthSyncingValidator {
	return &EthSyncingValidator{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (e *EthSyncingValidator) getSyncingStatus() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_syncing", nil, e.chain.Chain)
	if err != nil {
		return nil, err
	}

	response := e.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}

	return response.ResponseResult(), nil
}

func parseHexBig(s string) (*big.Int, bool) {
	s = strings.ToLower(s)
	if i := strings.Index(s, "x"); i >= 0 {
		s = s[i+1:]
	}
	return new(big.Int).SetString(s, 16)
}

var _ HealthValidator = (*EthSyncingValidator)(nil)
var _ HealthValidator = (*EthPeersValidator)(nil)
