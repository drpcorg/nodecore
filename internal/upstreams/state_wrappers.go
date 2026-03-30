package upstreams

import "github.com/drpcorg/nodecore/internal/protocol"

type ChainSupervisorStateWrapperEvent struct {
	Wrappers []ChainSupervisorStateWrapper
}

type ChainSupervisorStateWrapper interface {
	chainSupervisorEventWrapper()
}

type SubMethodsWrapper struct {
	SubMethods []string
}

func NewSubMethodsWrapper(subMethods []string) *SubMethodsWrapper {
	return &SubMethodsWrapper{SubMethods: subMethods}
}

func (s *SubMethodsWrapper) chainSupervisorEventWrapper() {}

type LabelsWrapper struct {
	Labels []AggregatedLabels
}

func NewLabelsWrapper(labels []AggregatedLabels) *LabelsWrapper {
	return &LabelsWrapper{Labels: labels}
}

func (l *LabelsWrapper) chainSupervisorEventWrapper() {}

type StatusWrapper struct {
	Status protocol.AvailabilityStatus
}

func NewStatusWrapper(status protocol.AvailabilityStatus) *StatusWrapper {
	return &StatusWrapper{
		Status: status,
	}
}

func (s *StatusWrapper) chainSupervisorEventWrapper() {}

type MethodsWrapper struct {
	Methods []string
}

func NewMethodsWrapper(methods []string) *MethodsWrapper {
	return &MethodsWrapper{
		Methods: methods,
	}
}

func (s *MethodsWrapper) chainSupervisorEventWrapper() {}

type BlocksWrapper struct {
	Blocks map[protocol.BlockType]protocol.Block
}

func NewBlocksWrapper(blocks map[protocol.BlockType]protocol.Block) *BlocksWrapper {
	return &BlocksWrapper{
		Blocks: blocks,
	}
}

func (s *BlocksWrapper) chainSupervisorEventWrapper() {}

type LowerBoundsWrapper struct {
	LowerBounds []protocol.LowerBoundData
}

func NewLowerBoundsWrapper(lowerBounds []protocol.LowerBoundData) *LowerBoundsWrapper {
	return &LowerBoundsWrapper{
		LowerBounds: lowerBounds,
	}
}

func (s *LowerBoundsWrapper) chainSupervisorEventWrapper() {}

type HeadWrapper struct {
	Head protocol.Block
}

func NewHeadWrapper(head protocol.Block) *HeadWrapper {
	return &HeadWrapper{
		Head: head,
	}
}

func (s *HeadWrapper) chainSupervisorEventWrapper() {}
