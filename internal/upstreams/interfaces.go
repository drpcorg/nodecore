package upstreams

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
)

type UpstreamSupervisor interface {
	GetChainSupervisor(chain chains.Chain) *ChainSupervisor
	GetChainSupervisors() []*ChainSupervisor
	GetUpstream(string) Upstream
	GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper]
	StartUpstreams()
}

type Upstream interface {
	Start()
	Resume()
	PartialStop()
	Stop()
	Running() bool
	PartialRunning() bool

	Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent]

	GetId() string
	GetChain() chains.Chain
	GetVendorType() UpstreamVendor
	GetUpstreamState() protocol.UpstreamState
	GetConnector(connectorType protocol.ApiConnectorType) connectors.ApiConnector
	GetHashIndex() string

	UpdateHead(height, slot uint64)
	UpdateBlock(block *protocol.BlockData, blockType protocol.BlockType)
	BanMethod(method string)
}
