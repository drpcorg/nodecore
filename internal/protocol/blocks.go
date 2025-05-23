package protocol

type Block struct {
	BlockData *BlockData
	RawBlock  []byte
}

type BlockData struct {
	Height uint64
	Slot   uint64
	Hash   string
}

func NewBlockDataWithHeight(height uint64) *BlockData {
	return &BlockData{Height: height}
}

func NewBlock(height, slot uint64, hash string, rawBlock []byte) *Block {
	return &Block{
		BlockData: &BlockData{
			Height: height,
			Slot:   slot,
			Hash:   hash,
		},
		RawBlock: rawBlock,
	}
}
