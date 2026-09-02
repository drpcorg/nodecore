package protocol

import (
	"fmt"
	"time"
)

type LowerBoundType int

const (
	UnknownBound LowerBoundType = iota + 1
	SlotBound
	StateBound
	ReceiptsBound
	TxBound
	BlockBound
	LogsBound
	TraceBound
	ProofBound
	EpochBound
	BlobBound
	// UpperProofBound is the newest block whose eth_getProof is served from a historical
	// proof store (op-reth debug_proofsSyncStatus "latest"). It is the only upper edge
	// among the bounds: it may move backwards, value 1 is not an archive marker, and
	// routing admits an upstream when the predicted value is >= the requested height.
	UpperProofBound
)

// IsUpperBound reports whether the type is the upper edge of a data window rather than
// a lower one. Processor, prediction, state and routing rules invert for it.
func (t LowerBoundType) IsUpperBound() bool {
	return t == UpperProofBound
}

func (t LowerBoundType) String() string {
	switch t {
	case SlotBound:
		return "SLOT"
	case UnknownBound:
		return "UNKNOWN"
	case StateBound:
		return "STATE"
	case ReceiptsBound:
		return "RECEIPTS"
	case TxBound:
		return "TX"
	case BlockBound:
		return "BLOCK"
	case LogsBound:
		return "LOGS"
	case TraceBound:
		return "TRACE"
	case ProofBound:
		return "PROOF"
	case EpochBound:
		return "EPOCH"
	case BlobBound:
		return "BLOB"
	case UpperProofBound:
		return "UPPER_PROOF"
	}
	panic(fmt.Sprintf("unknown lower bound type %d", t))
}

type LowerBoundData struct {
	Bound     int64
	Timestamp int64
	Type      LowerBoundType
}

func NewLowerBoundData(bound, timestamp int64, boundType LowerBoundType) LowerBoundData {
	return LowerBoundData{
		Bound:     bound,
		Timestamp: timestamp,
		Type:      boundType,
	}
}

func NewLowerBoundDataNow(bound int64, boundType LowerBoundType) LowerBoundData {
	return LowerBoundData{
		Bound:     bound,
		Timestamp: time.Now().Unix(),
		Type:      boundType,
	}
}
