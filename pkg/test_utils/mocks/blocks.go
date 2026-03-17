package mocks

import (
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/mock"
)

type BlockProcessorMock struct {
	mock.Mock

	subManager *utils.SubscriptionManager[blocks.BlockEvent]
}

func NewBlockProcessorMock() *BlockProcessorMock {
	return &BlockProcessorMock{
		subManager: utils.NewSubscriptionManager[blocks.BlockEvent]("block_processor_mock"),
	}
}

func (m *BlockProcessorMock) Start() {
	m.Called()
}

func (m *BlockProcessorMock) Stop() {
	m.Called()
}

func (m *BlockProcessorMock) Running() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *BlockProcessorMock) Subscribe(name string) *utils.Subscription[blocks.BlockEvent] {
	m.Called(name)
	return m.subManager.Subscribe(name)
}

func (m *BlockProcessorMock) UpdateBlock(blockData *protocol.BlockData, blockType protocol.BlockType) {
	m.Called(blockData, blockType)
}

func (m *BlockProcessorMock) DisabledBlocks() mapset.Set[protocol.BlockType] {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(mapset.Set[protocol.BlockType])
}

func (m *BlockProcessorMock) Publish(event blocks.BlockEvent) {
	m.subManager.Publish(event)
}

type HeadProcessorMock struct {
	mock.Mock

	subManager *utils.SubscriptionManager[blocks.HeadEvent]
}

func NewHeadProcessorMock() *HeadProcessorMock {
	return &HeadProcessorMock{
		subManager: utils.NewSubscriptionManager[blocks.HeadEvent]("head_processor_mock"),
	}
}

func (m *HeadProcessorMock) Start() {
	m.Called()
}

func (m *HeadProcessorMock) Stop() {
	m.Called()
}

func (m *HeadProcessorMock) Running() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *HeadProcessorMock) GetCurrentBlock() *protocol.Block {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*protocol.Block)
}

func (m *HeadProcessorMock) UpdateHead(height, slot uint64) {
	m.Called(height, slot)
}

func (m *HeadProcessorMock) Subscribe(name string) *utils.Subscription[blocks.HeadEvent] {
	m.Called(name)
	return m.subManager.Subscribe(name)
}

func (m *HeadProcessorMock) Publish(event blocks.HeadEvent) {
	m.subManager.Publish(event)
}
