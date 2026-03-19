package upstreams

import (
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/samber/lo"
)

type FilterUpstream func(id string, state *protocol.UpstreamState) bool
type SortUpstream func(entry1, entry2 lo.Tuple2[string, *protocol.UpstreamState]) int

type ChainSupervisor interface {
	Start()

	GetChain() chains.Chain
	GetChainState() ChainSupervisorState
	GetMethod(methodName string) *specs.Method
	GetMethods() []string
	GetUpstreamState(upstreamId string) *protocol.UpstreamState
	GetSortedUpstreamIds(filterFunc FilterUpstream, sortFunc SortUpstream) []string
	GetUpstreamIds() []string

	PublishUpstreamEvent(event protocol.UpstreamEvent)
}

type UpstreamSupervisor interface {
	GetChainSupervisor(chain chains.Chain) ChainSupervisor
	GetChainSupervisors() []ChainSupervisor
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

	Subscribe(name string) *utils.Subscription[protocol.UpstreamEvent]

	GetId() string
	GetChain() chains.Chain
	GetVendorType() UpstreamVendor
	GetUpstreamState() protocol.UpstreamState
	GetConnector(connectorType protocol.ApiConnectorType) connectors.ApiConnector
	GetHashIndex() string

	UpdateHead(height, slot uint64)
	UpdateBlock(block protocol.Block, blockType protocol.BlockType)
	BanMethod(method string)
}
