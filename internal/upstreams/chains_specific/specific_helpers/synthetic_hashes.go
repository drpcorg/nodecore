package specific_helpers

import (
	"encoding/binary"

	"github.com/drpcorg/nodecore/pkg/blockchain"
)

// SyntheticHashes builds a block id and a parent id from heights alone, for
// chains whose head-poll path exposes no real block hash (solana slots,
// stellar ledger sequences). Both ids are deterministic big-endian height
// encodings, so block(N).ParentHash always equals block(N-1).Hash and the
// parent-linkage checks in head-stream consumers hold.
func SyntheticHashes(height uint64, parentHeight uint64) (blockchain.HashId, blockchain.HashId) {
	b1 := make([]byte, 32)
	binary.BigEndian.PutUint64(b1, height)
	syntheticHash := blockchain.NewHashIdFromBytes(b1)

	b2 := make([]byte, 32)
	binary.BigEndian.PutUint64(b2, parentHeight)
	syntheticParentHash := blockchain.NewHashIdFromBytes(b2)

	return syntheticHash, syntheticParentHash
}
