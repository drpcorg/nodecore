package protocol

import "github.com/ethereum/go-ethereum/rpc"

type Block struct {
	Height    *rpc.BlockNumber
	Hash      string
	BlockJson []byte
}
