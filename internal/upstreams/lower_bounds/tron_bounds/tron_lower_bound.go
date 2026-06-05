package tron_bounds

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/lower_bounds"
	"github.com/drpcorg/nodecore/pkg/chains"
)

const tronLowerBoundPeriod = 3 * time.Minute

type TronLowerBoundDetector struct {
	*lower_bounds.LowerBoundSearchCalculator

	connector       connectors.ApiConnector
	chain           chains.Chain
	internalTimeout time.Duration
}

var tronSupportedBoundTypes = []protocol.LowerBoundType{
	protocol.BlockBound,
	protocol.StateBound,
	protocol.TxBound,
	protocol.ReceiptsBound,
}

func NewTronLowerBoundDetector(
	upstreamId string,
	chain chains.Chain,
	internalTimeout time.Duration,
	connector connectors.ApiConnector,
) *TronLowerBoundDetector {
	return &TronLowerBoundDetector{
		LowerBoundSearchCalculator: lower_bounds.NewLowerBoundSearchCalculatorWithSupportedTypes(
			upstreamId,
			protocol.BlockBound,
			tronSupportedBoundTypes,
			tronLowerBoundPeriod,
		),
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (t *TronLowerBoundDetector) DetectLowerBound() ([]protocol.LowerBoundData, error) {
	bounds, err := t.LowerBoundSearchCalculator.DetectLowerBound(t.fetchLatestHeight, t.probe)
	if err != nil {
		return nil, err
	}
	if len(bounds) == 0 {
		return bounds, nil
	}
	base := bounds[0]
	expanded := make([]protocol.LowerBoundData, 0, len(tronSupportedBoundTypes))
	for _, bt := range tronSupportedBoundTypes {
		expanded = append(expanded, protocol.LowerBoundData{
			Bound:     base.Bound,
			Timestamp: base.Timestamp,
			Type:      bt,
		})
	}
	return expanded, nil
}

func (t *TronLowerBoundDetector) fetchLatestHeight() (int64, error) {
	raw, err := t.callGetBlock(nil)
	if err != nil {
		return 0, err
	}
	number, err := parseTronBlockNumber(raw)
	if err != nil {
		return 0, fmt.Errorf("tron upstream '%s' latest block unparseable: %w", t.UpstreamId, err)
	}
	if number <= 0 {
		return 0, fmt.Errorf("tron upstream '%s' returned non-positive latest block number %d", t.UpstreamId, number)
	}
	return number, nil
}

func (t *TronLowerBoundDetector) probe(height int64) (bool, error) {
	body := []byte(fmt.Sprintf(`{"id_or_num":"%d","detail":false}`, height))
	raw, err := t.callGetBlock(body)
	if err != nil {
		return false, err
	}
	if isTronEmptyBlock(raw) {
		return false, nil
	}
	var parsed struct {
		BlockID string `json:"blockID"`
	}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		return false, fmt.Errorf("tron upstream '%s' /wallet/getblock body unparseable: %w", t.UpstreamId, err)
	}
	return parsed.BlockID != "", nil
}

func (t *TronLowerBoundDetector) callGetBlock(body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.internalTimeout)
	defer cancel()

	request := protocol.NewInternalUpstreamRestRequestWithBody("POST", "/wallet/getblock", body, t.chain)

	response := t.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	return response.ResponseResult(), nil
}

func isTronEmptyBlock(raw []byte) bool {
	return strings.TrimSpace(string(raw)) == "{}"
}

func parseTronBlockNumber(raw []byte) (int64, error) {
	var parsed struct {
		BlockHeader struct {
			RawData struct {
				Number int64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := sonic.Unmarshal(raw, &parsed); err != nil {
		return 0, err
	}
	return parsed.BlockHeader.RawData.Number, nil
}

var _ lower_bounds.LowerBoundDetector = (*TronLowerBoundDetector)(nil)
