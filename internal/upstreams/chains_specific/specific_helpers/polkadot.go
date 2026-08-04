package specific_helpers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

// PolkadotHeader is the subset of a substrate block header nodecore needs. The
// header does NOT contain its own hash - that requires a follow-up
// chain_getBlockHash(number) call.
type PolkadotHeader struct {
	ParentHash string `json:"parentHash"`
	Number     string `json:"number"`
}

var ErrPolkadotEmptyBlockHash = errors.New("polkadot node returned an empty block hash")

func IsJsonNull(raw []byte) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func FetchPolkadotHeader(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
) (*PolkadotHeader, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("chain_getHeader", []any{}, chain)
	if err != nil {
		return nil, err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return ParsePolkadotHeader(response.ResponseResult())
}

func ParsePolkadotHeader(raw []byte) (*PolkadotHeader, error) {
	var header PolkadotHeader
	if err := sonic.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("polkadot header payload unparseable: %w", err)
	}
	if header.Number == "" {
		return nil, fmt.Errorf("polkadot header has no number, got '%s'", string(raw))
	}
	return &header, nil
}

func ParsePolkadotHeight(number string) (uint64, error) {
	trimmed, ok := strings.CutPrefix(number, "0x")
	if !ok {
		trimmed, ok = strings.CutPrefix(number, "0X")
	}
	if !ok || trimmed == "" {
		return 0, fmt.Errorf("polkadot header number '%s' is not a 0x-prefixed hex number", number)
	}
	height, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("polkadot header number '%s' is not a hex number: %w", number, err)
	}
	return height, nil
}

// FetchPolkadotBlockHash resolves a header number to its block hash. number is
// passed through verbatim so the node sees the same representation it emitted.
func FetchPolkadotBlockHash(
	ctx context.Context,
	connector connectors.ApiConnector,
	chain chains.Chain,
	number string,
) (string, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("chain_getBlockHash", []any{number}, chain)
	if err != nil {
		return "", err
	}
	response := connector.SendRequest(ctx, request)
	if response.HasError() {
		return "", response.GetError()
	}
	result := response.ResponseResult()
	if IsJsonNull(result) {
		return "", ErrPolkadotEmptyBlockHash
	}
	hash := protocol.ResultAsString(result)
	if hash == "" {
		return "", ErrPolkadotEmptyBlockHash
	}
	return hash, nil
}
