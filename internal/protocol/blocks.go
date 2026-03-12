package protocol

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type Block struct {
	BlockData *BlockData
}

type BlockData struct {
	Height     uint64
	Slot       uint64
	Hash       blockchain.HashId
	ParentHash blockchain.HashId
}

func (b *BlockData) IsEmpty() bool {
	return b.Height == 0 && b.Slot == 0 && len(b.Hash) == 0 && len(b.ParentHash) == 0
}

func NewBlockDataWithHeight(height uint64) *BlockData {
	return &BlockData{Height: height}
}

func NewBlockData(height, slot uint64, hash, parentHash blockchain.HashId) *BlockData {
	return &BlockData{
		Height:     height,
		Slot:       slot,
		Hash:       hash,
		ParentHash: parentHash,
	}
}

func NewBlock(height, slot uint64, hash, parentHash blockchain.HashId) *Block {
	return &Block{
		BlockData: &BlockData{
			Height:     height,
			Slot:       slot,
			Hash:       hash,
			ParentHash: parentHash,
		},
	}
}

func NewBlockWithHeights(height, slot uint64) *Block {
	return &Block{
		BlockData: &BlockData{Height: height, Slot: slot},
	}
}

type BlockInfo struct {
	blocks *utils.CMap[BlockType, *BlockData]
}

func NewBlockInfo() *BlockInfo {
	return &BlockInfo{
		blocks: utils.NewCMap[BlockType, *BlockData](),
	}
}

func (b *BlockInfo) GetBlocks() map[BlockType]*BlockData {
	blocks := map[BlockType]*BlockData{}

	b.blocks.Range(func(key BlockType, val *BlockData) bool {
		blocks[key] = val
		return true
	})

	return blocks
}

func (b *BlockInfo) AddBlock(data *BlockData, blockType BlockType) {
	b.blocks.Store(blockType, data)
}

func (b *BlockInfo) GetBlock(blockType BlockType) *BlockData {
	block, ok := b.blocks.Load(blockType)
	if !ok {
		return &BlockData{}
	}
	return block
}

type LowerBoundInfo struct {
	lowerBounds mapset.Set[LowerBoundData]
}

func NewLowerBoundInfo() *LowerBoundInfo {
	return &LowerBoundInfo{
		lowerBounds: mapset.NewSet[LowerBoundData](),
	}
}

func (l *LowerBoundInfo) AddLowerBound(data LowerBoundData) {
	l.lowerBounds.Add(data)
}

func (l *LowerBoundInfo) GetAllBounds() []LowerBoundData {
	return l.lowerBounds.ToSlice()
}
