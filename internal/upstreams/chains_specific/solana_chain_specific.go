package specific

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

const checkInterval = 5

type SolanaChainSpecificObject struct {
	upstreamId       string
	connector        connectors.ApiConnector
	lastKnownHeights *utils.CMap[string, uint64]
	lastCheckedSlots *utils.CMap[string, uint64]
}

func NewSolanaChainSpecificObject(
	upstreamId string,
	connector connectors.ApiConnector,
) *SolanaChainSpecificObject {
	return &SolanaChainSpecificObject{
		upstreamId:       upstreamId,
		connector:        connector,
		lastKnownHeights: utils.NewCMap[string, uint64](),
		lastCheckedSlots: utils.NewCMap[string, uint64](),
	}
}

func (s *SolanaChainSpecificObject) SettingsValidators() []validations.SettingsValidator {
	return nil
}

func (s *SolanaChainSpecificObject) GetLatestBlock(ctx context.Context) (*protocol.Block, error) {
	return s.getEpochInfo(ctx)
}

func (s *SolanaChainSpecificObject) GetFinalizedBlock(_ context.Context) (*protocol.Block, error) {
	// TODO: implement get block/slot with finalized commitment
	return nil, nil
}

func (s *SolanaChainSpecificObject) ParseBlock(blockBytes []byte) (*protocol.Block, error) {
	epochInfo := SolanaEpochInfo{}
	err := sonic.Unmarshal(blockBytes, &epochInfo)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse the solana block, reason - %s", err.Error())
	}

	return createNewSolanaBlock(epochInfo.BlockHeight, epochInfo.AbsoluteSlot), nil
}

func (s *SolanaChainSpecificObject) ParseSubscriptionBlock(blockBytes []byte) (*protocol.Block, error) {
	slotEvent := SolanaSlotEvent{}
	err := sonic.Unmarshal(blockBytes, &slotEvent)
	if err != nil {
		return nil, err
	}
	lastSlot, _ := s.lastCheckedSlots.Load(s.upstreamId)
	lastHeight, _ := s.lastKnownHeights.Load(s.upstreamId)
	shouldCheck := slotEvent.Slot >= lastSlot && slotEvent.Slot-lastSlot >= checkInterval
	estimatedHeight := lo.Ternary(lastHeight != 0 && lastSlot != 0, lastHeight+(slotEvent.Slot-lastSlot), 0)

	if shouldCheck || estimatedHeight == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		block, err := s.getEpochInfo(ctx)
		if err != nil {
			var height uint64
			if estimatedHeight != 0 {
				height = estimatedHeight
			} else {
				if lastHeight != 0 {
					height = lastHeight
				} else {
					height = slotEvent.Slot
				}
			}
			log.Err(err).Msgf("couldn't get the epoch info for upstream %s, using the estimated height %d, slot %d", s.upstreamId, height, slotEvent.Slot)
			return createNewSolanaBlock(height, slotEvent.Slot), nil
		}
		return createNewSolanaBlock(block.BlockData.Height, block.BlockData.Slot), nil
	}
	return createNewSolanaBlock(estimatedHeight, slotEvent.Slot), nil
}

func (s *SolanaChainSpecificObject) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	return protocol.NewInternalSubUpstreamJsonRpcRequest("slotSubscribe", nil)
}

func (s *SolanaChainSpecificObject) getEpochInfo(ctx context.Context) (*protocol.Block, error) {
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("getEpochInfo", nil)
	if err != nil {
		return nil, err
	}
	response := s.connector.SendRequest(ctx, request)
	if response.HasError() {
		return nil, response.GetError()
	}
	block, err := s.ParseBlock(response.ResponseResult())
	if err != nil {
		return nil, err
	}

	s.lastKnownHeights.Store(s.upstreamId, block.BlockData.Height)
	s.lastCheckedSlots.Store(s.upstreamId, block.BlockData.Slot)

	return block, nil
}

func SyntheticHashes(slot uint64, parentSlot uint64) (blockchain.HashId, blockchain.HashId) {
	b1 := make([]byte, 32)
	binary.BigEndian.PutUint64(b1, slot)
	syntheticHash := blockchain.NewHashIdFromBytes(b1)

	b2 := make([]byte, 32)
	binary.BigEndian.PutUint64(b2, parentSlot)
	syntheticParentHash := blockchain.NewHashIdFromBytes(b2)

	return syntheticHash, syntheticParentHash
}

func createNewSolanaBlock(height uint64, slot uint64) *protocol.Block {
	hash, parentHash := SyntheticHashes(slot, slot-1)
	return protocol.NewBlock(height, slot, hash, parentHash)
}

type SolanaEpochInfo struct {
	AbsoluteSlot uint64 `json:"absoluteSlot"`
	BlockHeight  uint64 `json:"blockHeight"`
}

type SolanaSlotEvent struct {
	Slot uint64 `json:"slot"`
}

var _ ChainSpecific = (*SolanaChainSpecificObject)(nil)
