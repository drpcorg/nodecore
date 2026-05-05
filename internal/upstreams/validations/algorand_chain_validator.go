package validations

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errAlgorandEmptyGenesis = errors.New("algorand node returned empty genesis")

// AlgorandChainValidator pulls `GET /v2/genesis` and compares the reported
// network identity to the chain's configured chainId. Algorand has no
// EVM-style numeric chain id; the natural identifier is the genesis network
// (`mainnet`/`testnet`/`betanet`/...) plus the genesis schema id (`v1.0`).
// We accept any of: bare network, bare id, or composed `network-id` so
// existing config layouts continue to validate.
type AlgorandChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewAlgorandChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *AlgorandChainValidator {
	return &AlgorandChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AlgorandChainValidator) Validate() ValidationSettingResult {
	expected := strings.TrimSpace(a.chain.ChainId)
	if expected == "" {
		// chains.yaml entries for Algorand may omit chain-id (no canonical
		// numeric value). Skip validation cleanly rather than rejecting the
		// upstream.
		return Valid
	}
	genesis, err := a.fetchGenesis()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch algorand genesis for upstream '%s'", a.upstreamId)
		return SettingsError
	}
	candidates := genesisCandidates(genesis)
	for _, c := range candidates {
		if strings.EqualFold(c, expected) {
			return Valid
		}
	}
	log.Error().Msgf(
		"'%s' is configured with chain-id '%s' but algorand upstream '%s' reports network='%s' id='%s'",
		a.chain.Chain.String(),
		expected,
		a.upstreamId,
		genesis.Network,
		genesis.Id,
	)
	return FatalSettingError
}

func (a *AlgorandChainValidator) fetchGenesis() (*algorandGenesis, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequest("GET", "/v2/genesis", a.chain.Chain)

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	raw := response.ResponseResult()
	if len(raw) == 0 {
		return nil, errAlgorandEmptyGenesis
	}
	var genesis algorandGenesis
	if err := sonic.Unmarshal(raw, &genesis); err != nil {
		return nil, err
	}
	return &genesis, nil
}

func genesisCandidates(g *algorandGenesis) []string {
	candidates := make([]string, 0, 3)
	if g.Network != "" {
		candidates = append(candidates, g.Network)
	}
	if g.Id != "" {
		candidates = append(candidates, g.Id)
	}
	if g.Network != "" && g.Id != "" {
		candidates = append(candidates, g.Network+"-"+g.Id)
	}
	return candidates
}

type algorandGenesis struct {
	Network string `json:"network"`
	Id      string `json:"id"`
}

var _ SettingsValidator = (*AlgorandChainValidator)(nil)
