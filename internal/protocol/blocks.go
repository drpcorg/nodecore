package protocol

import "github.com/drpcorg/nodecore/pkg/blockchain"

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
