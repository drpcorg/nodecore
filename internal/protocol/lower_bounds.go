package protocol

import (
	"fmt"
	"time"
)

type LowerBoundType int

const (
	UnknownBound LowerBoundType = iota
	SlotBound
	StateBound
	ReceiptsBound
	TxBound
	BlockBound
)

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
