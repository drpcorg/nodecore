package protocol

type Block struct {
	Height    uint64
	Slot      uint64
	Hash      string
	BlockJson []byte
}
