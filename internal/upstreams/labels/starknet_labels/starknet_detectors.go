package starknet_labels

import (
	"strings"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// StarknetClientLabelsDetector probes one Starknet client: pathfinder answers
// `pathfinder_version`, juno answers `juno_version`, each returns -32601 for the
// other's method, so the two detectors are chained as fallbacks in one handler.
type StarknetClientLabelsDetector struct {
	chain      chains.Chain
	method     string
	clientType string
}

func NewPathfinderClientLabelsDetector(chain chains.Chain) *StarknetClientLabelsDetector {
	return &StarknetClientLabelsDetector{chain: chain, method: "pathfinder_version", clientType: "pathfinder"}
}

func NewJunoClientLabelsDetector(chain chains.Chain) *StarknetClientLabelsDetector {
	return &StarknetClientLabelsDetector{chain: chain, method: "juno_version", clientType: "juno"}
}

func (s *StarknetClientLabelsDetector) NodeTypeRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalUpstreamJsonRpcRequest(s.method, []any{}, s.chain)
}

// ClientVersionAndType reads the quoted bare version (`"v0.16.4"`); any other result
// keeps the client type and drops the version, never an error: a node that answered the
// probe is this client, whatever it put in the result.
func (s *StarknetClientLabelsDetector) ClientVersionAndType(data []byte) (string, string, error) {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		log.Warn().Msgf("unexpected '%s' result '%s', skipping client_version", s.method, data)
		return "", s.clientType, nil
	}
	return strings.TrimPrefix(protocol.ResultAsString(data), "v"), s.clientType, nil
}

var _ labels.ClientLabelsDetector = (*StarknetClientLabelsDetector)(nil)
