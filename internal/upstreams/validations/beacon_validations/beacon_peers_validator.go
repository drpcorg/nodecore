package beacon_validations

import (
	"context"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// BeaconChainPeersValidator probes GET /eth/v1/node/peer_count and reports the
// upstream Immature while its connected-peer count is below the configured
// minimum, otherwise Available. The beacon API returns the counts as strings
// ({"data":{"connected":"64", ...}}).
type BeaconChainPeersValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	minPeers        int64
	internalTimeout time.Duration
}

func NewBeaconChainPeersValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	minPeers int64,
	internalTimeout time.Duration,
) *BeaconChainPeersValidator {
	return &BeaconChainPeersValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		minPeers:        minPeers,
		internalTimeout: internalTimeout,
	}
}

func (b *BeaconChainPeersValidator) Validate() protocol.AvailabilityStatus {
	connected, err := b.fetchConnectedPeers()
	if err != nil {
		log.Error().Err(err).Msgf("beacon upstream '%s' peer-count validation failed", b.upstreamId)
		return protocol.Unavailable
	}
	if connected < b.minPeers {
		return protocol.Immature
	}
	return protocol.Available
}

func (b *BeaconChainPeersValidator) fetchConnectedPeers() (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET#/eth/v1/node/peer_count", nil, b.chain)

	response := b.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	var peers struct {
		Data struct {
			Connected string `json:"connected"`
		} `json:"data"`
	}
	if err := sonic.Unmarshal(response.ResponseResult(), &peers); err != nil {
		return 0, err
	}
	return strconv.ParseInt(peers.Data.Connected, 10, 64)
}

var _ validations.HealthValidator = (*BeaconChainPeersValidator)(nil)
