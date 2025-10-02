package protocol

type Block struct {
	BlockData *BlockData
}

type BlockData struct {
	Height uint64
	Slot   uint64
	Hash   string
}

func (b *BlockData) IsEmpty() bool {
	return b.Height == 0 && b.Slot == 0 && b.Hash == ""
}

func NewBlockDataWithHeight(height uint64) *BlockData {
	return &BlockData{Height: height}
}

func NewBlockData(height, slot uint64, hash string) *BlockData {
	return &BlockData{Height: height, Slot: slot, Hash: hash}
}

func NewBlock(height, slot uint64, hash string) *Block {
	return &Block{
		BlockData: &BlockData{
			Height: height,
			Slot:   slot,
			Hash:   hash,
		},
	}
}
