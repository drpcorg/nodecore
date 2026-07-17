package starknet_validations

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/rs/zerolog/log"
)

// no peer validation: juno syncs from the feeder gateway, there is no p2p peer count
type StarknetHealthValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
}

func NewStarknetHealthValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	internalTimeout time.Duration,
) *StarknetHealthValidator {
	return &StarknetHealthValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: internalTimeout,
	}
}

func (s *StarknetHealthValidator) Validate() protocol.AvailabilityStatus {
	syncStatus, err := s.fetchSyncStatus()
	if err != nil {
		log.Error().Err(err).Msgf("starknet upstream '%s' health validation failed", s.upstreamId)
		return protocol.Unavailable
	}
	if syncStatus.syncObject == nil {
		// some upstreams return a bare boolean instead of the sync object
		if syncStatus.syncing {
			log.Warn().Msgf("starknet upstream '%s' is syncing", s.upstreamId)
			return protocol.Syncing
		}
		return protocol.Available
	}
	lag := int64(syncStatus.syncObject.HighestBlockNum) - int64(syncStatus.syncObject.CurrentBlockNum)
	if s.chain.Settings.Lags.Syncing > 0 && lag > s.chain.Settings.Lags.Syncing {
		log.Warn().Msgf(
			"starknet upstream '%s' is syncing, current_block_num=%d highest_block_num=%d",
			s.upstreamId,
			uint64(syncStatus.syncObject.CurrentBlockNum),
			uint64(syncStatus.syncObject.HighestBlockNum),
		)
		return protocol.Syncing
	}
	return protocol.Available
}

type starknetSyncStatus struct {
	syncing    bool
	syncObject *starknetSyncObject
}

type starknetSyncObject struct {
	StartingBlockNum starknetBlockNum `json:"starting_block_num"`
	CurrentBlockNum  starknetBlockNum `json:"current_block_num"`
	HighestBlockNum  starknetBlockNum `json:"highest_block_num"`
}

// starknet_syncing block nums are hex strings in the spec but plain numbers on some clients
type starknetBlockNum uint64

func (n *starknetBlockNum) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	base := 10
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		raw = raw[2:]
		base = 16
	}
	value, err := strconv.ParseUint(raw, base, 64)
	if err != nil {
		return fmt.Errorf("invalid starknet block num %s: %w", string(data), err)
	}
	*n = starknetBlockNum(value)
	return nil
}

func (s *StarknetHealthValidator) fetchSyncStatus() (*starknetSyncStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("starknet_syncing", []any{}, s.chain.Chain)
	if err != nil {
		return nil, err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	result := response.ResponseResult()
	var syncing bool
	if err := sonic.Unmarshal(result, &syncing); err == nil {
		return &starknetSyncStatus{syncing: syncing}, nil
	}
	var syncObject starknetSyncObject
	if err := sonic.Unmarshal(result, &syncObject); err != nil {
		return nil, fmt.Errorf("unexpected starknet_syncing result '%s': %w", protocol.ResultAsString(result), err)
	}
	return &starknetSyncStatus{syncObject: &syncObject}, nil
}

var _ validations.HealthValidator = (*StarknetHealthValidator)(nil)
