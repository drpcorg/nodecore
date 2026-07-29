package protocol

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/samber/lo"
)

type AbstractUpstreamStateEvent interface {
	ProcessEvent(state UpstreamState) UpstreamState
	Same(state UpstreamState) bool
}

type InitUpstreamStateEvent struct {
}

func (i *InitUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

func (i *InitUpstreamStateEvent) Same(state UpstreamState) bool {
	return false
}

type LowerBoundUpstreamStateEvent struct {
	Data LowerBoundData
}

func (l *LowerBoundUpstreamStateEvent) Same(state UpstreamState) bool {
	lowerBound, ok := state.LowerBoundsInfo.GetLowerBound(l.Data.Type)
	if !ok {
		return false
	}
	return lowerBound == l.Data
}

func (l *LowerBoundUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	copyLowerBoundInfo := state.LowerBoundsInfo.Copy()
	copyLowerBoundInfo.AddLowerBound(l.Data)

	state.LowerBoundsInfo = copyLowerBoundInfo
	return state
}

type StatusUpstreamStateEvent struct {
	// Status is the base availability reported by a health probe (setStatus).
	// It is ignored when Lag != nil.
	Status AvailabilityStatus
	// Lag is a discriminator, not the source of truth: non-nil marks a head-lag
	// update (setLag) that only asks the event loop to re-derive availability from
	// the upstream's current lag (stored on the upstream itself); nil
	// marks a setStatus that updates the base availability. A pointer is used so
	// this stays distinguishable from a setStatus even when the lag is 0.
	Lag *int64
}

func (s *StatusUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

// ProcessEvent is a no-op: the setStatus/setLag mutation and the derived
// effective availability are handled by processStateEvents, which owns both the
// base-availability tracking and the per-chain syncing threshold. It exists only
// to satisfy AbstractUpstreamStateEvent.
func (s *StatusUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

type FatalErrorUpstreamStateEvent struct{}

func (f *FatalErrorUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

func (f *FatalErrorUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

type ValidUpstreamStateEvent struct{}

func (v *ValidUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

func (v *ValidUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

type HeadUpstreamStateEvent struct {
	HeadData Block
}

func (h *HeadUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

func (h *HeadUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	state.HeadData = h.HeadData
	return state
}

type BlockUpstreamStateEvent struct {
	Block     Block
	BlockType BlockType
}

func (b *BlockUpstreamStateEvent) Same(state UpstreamState) bool {
	block := state.BlockInfo.GetBlock(b.BlockType)
	return block.Equals(b.Block)
}

func (b *BlockUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	copyBlockInfo := state.BlockInfo.Copy()
	copyBlockInfo.AddBlock(b.Block, b.BlockType)

	state.BlockInfo = copyBlockInfo
	return state
}

type BanMethodUpstreamStateEvent struct {
	Method string
}

func (b *BanMethodUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

func (b *BanMethodUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

type UnbanMethodUpstreamStateEvent struct {
	Method string
}

func (u *UnbanMethodUpstreamStateEvent) Same(_ UpstreamState) bool {
	return false
}

func (u *UnbanMethodUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	return state
}

// CapsUpstreamStateEvent replaces the upstream's capability set wholesale. Caps are
// produced by the per-chain cap pipeline (caps.CapProcessor aggregating CapDetectors),
// which already merges every detector's view into one set, so the event carries the
// full set rather than incremental add/remove.
type CapsUpstreamStateEvent struct {
	Caps mapset.Set[Cap]
}

func (c *CapsUpstreamStateEvent) Same(state UpstreamState) bool {
	return state.Caps != nil && c.Caps != nil && state.Caps.Equal(c.Caps)
}

func (c *CapsUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	state.Caps = c.Caps.Clone()
	return state
}

type LabelsUpstreamStateEvent struct {
	Labels lo.Tuple2[string, string]
}

func (l *LabelsUpstreamStateEvent) Same(state UpstreamState) bool {
	label, ok := state.Labels.GetLabel(l.Labels.A)
	if !ok {
		return false
	}
	return label == l.Labels.B
}

func (l *LabelsUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	copyLabels := state.Labels.Copy()
	copyLabels.AddLabel(l.Labels.A, l.Labels.B)
	state.Labels = copyLabels
	return state
}

var _ AbstractUpstreamStateEvent = (*LabelsUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*CapsUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*UnbanMethodUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*BanMethodUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*BlockUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*HeadUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*ValidUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*FatalErrorUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*StatusUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*LowerBoundUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*InitUpstreamStateEvent)(nil)
