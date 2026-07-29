package starknet_labels

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// The client-identity probe pair mirrors dshackle: pathfinder answers
// `pathfinder_version`, juno answers `juno_version`, each returns -32601
// for the other's method.
var versionProbes = []struct {
	method     string
	clientType string
}{
	{method: "pathfinder_version", clientType: "pathfinder"},
	{method: "juno_version", clientType: "juno"},
}

// StarknetClientLabelsDetector implements labels.LabelsDetector directly
// because the single-request labels.ClientLabelsDetector interface cannot
// express the two-probe pathfinder/juno fallback chain.
type StarknetClientLabelsDetector struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

func NewStarknetLabelsDetector(
	upstreamId string,
	connector connectors.ApiConnector,
	chain chains.Chain,
	internalTimeout time.Duration,
) *StarknetClientLabelsDetector {
	return &StarknetClientLabelsDetector{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StarknetClientLabelsDetector) DetectLabels() map[string]string {
	for _, probe := range versionProbes {
		request, err := protocol.NewInternalUpstreamJsonRpcRequest(probe.method, []any{}, s.chain)
		if err != nil {
			log.Error().Err(err).Msgf("failed to create a '%s' client labels request of '%s'", probe.method, s.upstreamId)
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
		response := s.connector.SendRequest(ctx, request)
		cancel()

		if response.HasError() {
			// -32601 on the wrong client, fall through to the next probe
			log.Debug().Err(response.GetError()).Msgf("'%s' client labels probe of '%s' failed", probe.method, s.upstreamId)
			continue
		}

		clientLabels := map[string]string{"client_type": probe.clientType}
		// version is a quoted bare string like "v0.16.4"
		result := response.ResponseResult()
		if len(result) >= 2 && result[0] == '"' && result[len(result)-1] == '"' {
			clientVersion := strings.TrimPrefix(protocol.ResultAsString(result), "v")
			if clientVersion != "" {
				clientLabels["client_version"] = clientVersion
			}
		} else {
			log.Warn().Msgf("unexpected '%s' result '%s' of '%s', skipping client_version", probe.method, result, s.upstreamId)
		}
		return clientLabels
	}

	log.Warn().Msgf("unable to detect a starknet client of '%s', neither pathfinder_version nor juno_version answered", s.upstreamId)
	return nil
}

var _ labels.LabelsDetector = (*StarknetClientLabelsDetector)(nil)
