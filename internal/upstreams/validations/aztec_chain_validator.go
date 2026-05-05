package validations

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

var errAztecEmptyChainId = errors.New("aztec node returned empty chain id")

type AztecChainValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewAztecChainValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *AztecChainValidator {
	return &AztecChainValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (a *AztecChainValidator) Validate() ValidationSettingResult {
	chainId, err := a.getChainId()
	if err != nil {
		log.Error().Err(err).Msgf("failed to get chainId of chain %s upstream '%s'", a.chain.Chain, a.upstreamId)
		return SettingsError
	}
	if chainId == "" {
		log.Error().Msgf("aztec upstream '%s' returned empty chain id", a.upstreamId)
		return SettingsError
	}
	if !chainIdEqual(chainId, a.chain.ChainId) {
		log.Error().Msgf(
			"'%s' is specified for upstream '%s' with chainId '%s', but the node reports chainId '%s'",
			a.chain.Chain.String(),
			a.upstreamId,
			a.chain.ChainId,
			chainId,
		)
		return FatalSettingError
	}
	return Valid
}

func (a *AztecChainValidator) getChainId() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), a.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("node_getChainId", []string{}, a.chain.Chain)
	if err != nil {
		return "", err
	}

	response := a.connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}

	raw := response.ResponseResult()
	if len(raw) == 0 {
		return "", errAztecEmptyChainId
	}

	// node_getChainId may return either a JSON number (e.g. 1) or a JSON string ("1" / "0x1")
	var asString string
	if errStr := sonic.Unmarshal(raw, &asString); errStr == nil {
		return strings.TrimSpace(asString), nil
	}
	var asNumber uint64
	errNum := sonic.Unmarshal(raw, &asNumber)
	if errNum == nil {
		return strings.ToLower((&big.Int{}).SetUint64(asNumber).String()), nil
	}
	// Neither shape decoded - surface the raw payload and the last decode error
	// so operators can diagnose misbehaving upstreams returning unexpected shapes.
	return "", fmt.Errorf("aztec chain id payload %q is not a JSON string or number: %w", string(raw), errNum)
}

// chainIdEqual compares two chainId strings tolerating decimal/hex formats and 0x prefix.
func chainIdEqual(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	an, aok := parseChainIdString(a)
	bn, bok := parseChainIdString(b)
	return aok && bok && an.Cmp(bn) == 0
}

func parseChainIdString(value string) (*big.Int, bool) {
	v := strings.TrimSpace(strings.ToLower(value))
	if v == "" {
		return nil, false
	}
	base := 10
	if strings.HasPrefix(v, "0x") {
		v = strings.TrimPrefix(v, "0x")
		base = 16
	}
	n, ok := new(big.Int).SetString(v, base)
	return n, ok
}

var _ SettingsValidator = (*AztecChainValidator)(nil)
