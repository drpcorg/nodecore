package protocol

import (
	"fmt"

	"github.com/samber/lo"
)

type AbstractUpstreamStateEvent interface {
	ProcessEvent(state UpstreamState) UpstreamState
	Same(state UpstreamState) bool
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
	Status AvailabilityStatus
}

func (s *StatusUpstreamStateEvent) Same(state UpstreamState) bool {
	return s.Status == state.Status
}

func (s *StatusUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	state.Status = s.Status
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

type SubscribeUpstreamStateEvent struct {
	State SubscribeConnectorState
}

func (s *SubscribeUpstreamStateEvent) Same(state UpstreamState) bool {
	switch s.State {
	case WsConnected:
		return state.Caps.Contains(WsCap)
	case WsDisconnected:
		return !state.Caps.Contains(WsCap)
	}
	panic(fmt.Sprintf("unexpected state: %T", s.State))
}

func (s *SubscribeUpstreamStateEvent) ProcessEvent(state UpstreamState) UpstreamState {
	copyCaps := state.Caps.Clone()
	switch s.State {
	case WsConnected:
		copyCaps.Add(WsCap)
	case WsDisconnected:
		copyCaps.Remove(WsCap)
	}
	state.Caps = copyCaps
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
var _ AbstractUpstreamStateEvent = (*SubscribeUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*UnbanMethodUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*BanMethodUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*BlockUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*HeadUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*ValidUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*FatalErrorUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*StatusUpstreamStateEvent)(nil)
var _ AbstractUpstreamStateEvent = (*LowerBoundUpstreamStateEvent)(nil)
