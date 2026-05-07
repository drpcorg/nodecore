package labels

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/errgroup"
)

const (
	hlBlocksToCheck  = 300
	hlMaxConcurrency = 32
	mainnetAddress   = "0x2222222222222222222222222222222222222222"
	testnetAddress   = "0x6ed35e7d6de4b45f4efb8a91eff31afa49362569"
)

var errHLNativeTxFound = errors.New("hl native tx found")

type EthHLTxLabelsDetector struct {
	upstreamId      string
	chain           chains.Chain
	connector       connectors.ApiConnector
	internalTimeout time.Duration

	detectCount atomic.Int64
}

func (e *EthHLTxLabelsDetector) DetectLabels() map[string]string {
	e.detectCount.Add(1)

	var nativeTxFrom string
	switch e.chain {
	case chains.HYPERLIQUID:
		nativeTxFrom = mainnetAddress
	case chains.HYPERLIQUID_TESTNET:
		nativeTxFrom = testnetAddress
	default:
		return nil
	}

	if e.detectCount.Load()%5 != 1 {
		return nil
	}

	latest, err := e.fetchLatestBlock()
	if err != nil {
		log.Error().Err(err).Msgf("unable to get the latest block to detect HL tx status of upstream '%s'", e.upstreamId)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(hlMaxConcurrency)

	for offset := int64(0); offset < hlBlocksToCheck; offset++ {
		g.Go(func() error {
			blockNum := new(big.Int).Sub(latest, big.NewInt(offset))
			if e.blockHasNativeTx(ctx, "0x"+blockNum.Text(16), nativeTxFrom) {
				return errHLNativeTxFound // short-circuit: cancels remaining probes, a native tx has been found
			}
			return nil
		})
	}

	if errors.Is(g.Wait(), errHLNativeTxFound) {
		return map[string]string{
			"include_hl_native_tx": "true",
			"exclude_hl_native_tx": "false",
		}
	}
	return map[string]string{
		"include_hl_native_tx": "false",
		"exclude_hl_native_tx": "true",
	}
}

func NewEthHLTxLabelsDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *EthHLTxLabelsDetector {
	return &EthHLTxLabelsDetector{
		upstreamId:      upstreamId,
		chain:           chain,
		connector:       connector,
		internalTimeout: internalTimeout,
	}
}

func (e *EthHLTxLabelsDetector) fetchLatestBlock() (*big.Int, error) {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_blockNumber", nil, e.chain)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.internalTimeout)
	defer cancel()

	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		return nil, resp.GetError()
	}

	respStr, err := resp.ResponseResultString()
	if err != nil {
		return nil, err
	}

	n, ok := new(big.Int).SetString(strings.TrimPrefix(strings.ToLower(respStr), "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("invalid block number %s", respStr)
	}
	return n, nil
}

func (e *EthHLTxLabelsDetector) blockHasNativeTx(ctx context.Context, blockHex, nativeTxFrom string) bool {
	req, err := protocol.NewInternalUpstreamJsonRpcRequest("eth_getBlockReceipts", []any{blockHex}, e.chain)
	if err != nil {
		log.Error().Err(err).Msgf("unable to create a request eth_getBlockReceipts to detect HL tx status of upstream '%s'", e.upstreamId)
		return false
	}
	resp := e.connector.SendRequest(ctx, req)
	if resp.HasError() {
		if resp.GetError().Code != protocol.CtxErrorCode {
			log.Error().Err(resp.GetError()).Msgf("unable to detect HL tx status of upstream '%s'", e.upstreamId)
		}
		return false
	}

	var receipts []struct {
		From string `json:"from"`
	}
	if err := sonic.Unmarshal(resp.ResponseResult(), &receipts); err != nil {
		log.Error().Err(err).Msgf("unable to parse a HL tx status response of upstream '%s'", e.upstreamId)
		return false
	}

	for _, r := range receipts {
		if r.From == nativeTxFrom {
			return true
		}
	}
	return false
}

var _ LabelsDetector = (*EthHLTxLabelsDetector)(nil)
