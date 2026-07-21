package near_validations

import (
	"context"
	"errors"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errNearNoPeers = errors.New("near node has no active peers")

// NearPeersValidator checks network connectivity via `network_info`.
type NearPeersValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewNearPeersValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *NearPeersValidator {
	return &NearPeersValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (n *NearPeersValidator) Validate() protocol.AvailabilityStatus {
	peers, err := n.fetchActivePeers()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch near network info for upstream '%s'", n.upstreamId)
		return protocol.Unavailable
	}
	if peers == 0 {
		log.Error().Err(errNearNoPeers).Msgf("near upstream '%s' has no active peers", n.upstreamId)
		return protocol.Unavailable
	}
	return protocol.Available
}

func (n *NearPeersValidator) fetchActivePeers() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), n.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("network_info", []any{}, n.chain.Chain)
	if err != nil {
		return 0, err
	}
	response := n.connector.SendRequest(ctx, request)
	if response.HasError() {
		return 0, response.GetError()
	}
	var info nearNetworkInfo
	if err := sonic.Unmarshal(response.ResponseResult(), &info); err != nil {
		return 0, err
	}
	return info.NumActivePeers, nil
}

type nearNetworkInfo struct {
	NumActivePeers uint64 `json:"num_active_peers"`
}

var _ validations.HealthValidator = (*NearPeersValidator)(nil)
