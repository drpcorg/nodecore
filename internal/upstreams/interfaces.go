package upstreams

import (
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/failsafe-go/failsafe-go"
	"github.com/samber/lo"
)

type ChainSupervisorEvent interface {
	chainSupervisorEvent()
}

type AddChainSupervisorEvent struct {
	ChainSupervisor ChainSupervisor
}

func (e *AddChainSupervisorEvent) chainSupervisorEvent() {}

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
	NextIndex() uint64

	PublishUpstreamEvent(event protocol.UpstreamEvent)
	SubscribeState(name string) *utils.Subscription[*ChainSupervisorStateWrapperEvent]
	// SubscribeHead is a head feed over the upstreams that are Available and pass
	// filter, re-evaluated on every upstream event. See HeadFeedEvent.
	SubscribeHead(name string, filter FilterUpstream) *HeadFeedSubscription
}

// UpstreamsDiff is what one ApplyUpstreams did, by upstream id. A changed upstream is one
// whose effective config differs from the running one: it is stopped and started anew. A
// failed one could not be created from its config; it is not running, and if it was running
// before, the old instance was left as it was.
type UpstreamsDiff struct {
	Added   []string
	Removed []string
	Changed []string
	Failed  []string
}

func (d UpstreamsDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 && len(d.Failed) == 0
}

func (d UpstreamsDiff) String() string {
	return fmt.Sprintf("added %v, removed %v, changed %v, failed %v", d.Added, d.Removed, d.Changed, d.Failed)
}

type UpstreamSupervisor interface {
	GetChainSupervisor(chain chains.Chain) ChainSupervisor
	GetChainSupervisors() []ChainSupervisor
	GetUpstream(string) Upstream
	GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper]
	StartUpstreams()
	// ApplyUpstreams reconciles the running upstreams with confs, which are effective
	// (post-defaults, validated) upstream configs: it starts the new ones, removes the
	// missing ones and replaces the changed ones. Removals finish before additions start.
	ApplyUpstreams(confs []*config.Upstream) UpstreamsDiff

	SubscribeChainSupervisor(name string) *utils.Subscription[ChainSupervisorEvent]
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
	GetGroupLabels() mapset.Set[string]
	GetVendorType() UpstreamVendor
	GetUpstreamState() protocol.UpstreamState
	GetConnector(connectorType specs.ApiConnectorType) connectors.ApiConnector
	GetHashIndex() string
	GetCurrentHeadHeight() uint64
	PredictLowerBound(boundType protocol.LowerBoundType, timeOffset int64) int64

	UpdateHead(height, slot uint64)
	UpdateHeadLag(lag int64)
	UpdateBlock(block protocol.Block, blockType protocol.BlockType)
	UpdateLowerBound(data protocol.LowerBoundData)
	BanMethod(method string)
}
