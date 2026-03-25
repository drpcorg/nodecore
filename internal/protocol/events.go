package protocol

import "github.com/samber/lo"

type AbstractUpstreamStateEvent interface {
	event()
}

type LowerBoundUpstreamStateEvent struct {
	Data LowerBoundData
}

func (l *LowerBoundUpstreamStateEvent) event() {
}

type StatusUpstreamStateEvent struct {
	Status AvailabilityStatus
}

func (s *StatusUpstreamStateEvent) event() {}

type FatalErrorUpstreamStateEvent struct{}

func (f *FatalErrorUpstreamStateEvent) event() {}

type ValidUpstreamStateEvent struct{}

func (f *ValidUpstreamStateEvent) event() {}

type HeadUpstreamStateEvent struct {
	HeadData Block
}

func (h *HeadUpstreamStateEvent) event() {}

type BlockUpstreamStateEvent struct {
	Block     Block
	BlockType BlockType
}

func (f *BlockUpstreamStateEvent) event() {
}

type BanMethodUpstreamStateEvent struct {
	Method string
}

func (f *BanMethodUpstreamStateEvent) event() {}

type UnbanMethodUpstreamStateEvent struct {
	Method string
}

func (f *UnbanMethodUpstreamStateEvent) event() {}

type SubscribeUpstreamStateEvent struct {
	State SubscribeConnectorState
}

func (s *SubscribeUpstreamStateEvent) event() {}

type LabelsUpstreamStateEvent struct {
	Labels lo.Tuple2[string, string]
}

func (l *LabelsUpstreamStateEvent) event() {}
