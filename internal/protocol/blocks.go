package protocol

import (
	"cmp"
	"reflect"

	"github.com/drpcorg/nodecore/pkg/blockchain"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type ZeroBlock = Block

type Block struct {
	Height     uint64
	Slot       uint64
	Hash       blockchain.HashId
	ParentHash blockchain.HashId
}

func (b Block) Equals(other Block) bool {
	return reflect.DeepEqual(b, other)
}

func (b Block) CompareWithHeight(other Block) int {
	return cmp.Compare(b.Height, other.Height)
}

func (b Block) IsEmptyByHeight() bool {
	return b.Height == 0 && len(b.Hash) == 0 && len(b.ParentHash) == 0
}

func (b Block) IsFullEmpty() bool {
	return b.Height == 0 && b.Slot == 0 && len(b.Hash) == 0 && len(b.ParentHash) == 0
}

func NewBlockWithHeight(height uint64) Block {
	return Block{Height: height}
}

func NewBlock(height, slot uint64, hash, parentHash blockchain.HashId) Block {
	return Block{
		Height:     height,
		Slot:       slot,
		Hash:       hash,
		ParentHash: parentHash,
	}
}

func NewBlockWithHeights(height, slot uint64) Block {
	return Block{
		Height: height, Slot: slot,
	}
}

type BlockInfo struct {
	blocks *utils.CMap[BlockType, Block]
}

func NewBlockInfo() *BlockInfo {
	return &BlockInfo{
		blocks: utils.NewCMap[BlockType, Block](),
	}
}

func (b *BlockInfo) GetBlocks() map[BlockType]Block {
	blocks := make(map[BlockType]Block, 6)

	b.blocks.Range(func(key BlockType, val Block) bool {
		blocks[key] = val
		return true
	})

	return blocks
}

func (b *BlockInfo) AddBlock(data Block, blockType BlockType) {
	b.blocks.Store(blockType, data)
}

func (b *BlockInfo) GetBlock(blockType BlockType) Block {
	block, ok := b.blocks.Load(blockType)
	if !ok {
		return Block{}
	}
	return block
}

type LowerBoundInfo struct {
	lowerBounds *utils.CMap[LowerBoundType, LowerBoundData]
}

func NewLowerBoundInfo() *LowerBoundInfo {
	return &LowerBoundInfo{
		lowerBounds: utils.NewCMap[LowerBoundType, LowerBoundData](),
	}
}

func (l *LowerBoundInfo) AddLowerBound(data LowerBoundData) {
	l.lowerBounds.Store(data.Type, data)
}

func (l *LowerBoundInfo) GetAllBounds() []LowerBoundData {
	bounds := make([]LowerBoundData, 0)

	l.lowerBounds.Range(func(key LowerBoundType, val LowerBoundData) bool {
		bounds = append(bounds, val)
		return true
	})

	return bounds
}
