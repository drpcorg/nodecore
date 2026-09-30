package emerald

import (
	"cmp"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/public/pkg/dshackle"
	"google.golang.org/protobuf/proto"
)

// Both status APIs share wakeups, throttling, cancellation and serialized sends.
func SubscribeNodeGroupStatusWithResync(supervisor upstreams.UpstreamSupervisor, request *dshackle.SubscribeNodeGroupStatusRequest, stream dshackle.Blockchain_SubscribeNodeGroupStatusServer, interval, resync time.Duration) error {
	if supervisor == nil {
		return errNilUpstreamSupervisor
	}
	producer := &upstreamStatusProducer{groupStream: stream, fullSeparation: request.GetFullSeparation(), requested: mapset.NewThreadUnsafeSet(request.GetChains()...), interval: interval, resyncInterval: resync}
	return producer.subscribe(supervisor, stream.Context())
}

func (c *upstreamStatusChain) groupResponse(supervisor upstreams.ChainSupervisor, now time.Time, resync time.Duration, fullSeparation bool) *dshackle.SubscribeNodeGroupStatusResponse {
	groups := upstreams.NodeGroups(supervisor, fullSeparation)
	if !c.announced && len(groups) == 0 {
		return nil
	}
	full := !c.announced || !now.Before(c.nextFull)
	network := toFullResponse(int(c.ref), supervisor.GetChainState()).ChainDescription
	canonicalGroupEvents(network.ChainEvent)
	response := &dshackle.SubscribeNodeGroupStatusResponse{Chain: c.ref, FullResponse: full, Network: network}
	current := make(map[string]*dshackle.NodeGroupStatus, len(groups))
	for _, id := range slices.Sorted(maps.Keys(groups)) {
		group := groups[id]
		description := toFullResponse(int(c.ref), group.State).ChainDescription
		canonicalGroupEvents(description.ChainEvent)
		wire := &dshackle.NodeGroupStatus{NodeGroupId: id, Status: ChainStatusToApi(group.State.Status).GetStatus(), Head: HeadToApi(group.State.HeadData.Head).GetHead(), Description: description.ChainEvent, UpstreamIndices: group.Indices}
		current[id] = wire
		if full || !proto.Equal(wire, c.sentGroups[id]) {
			response.Groups = append(response.Groups, wire)
		}
	}
	if !full {
		for id := range c.sentGroups {
			if current[id] == nil {
				response.RemovedNodeGroupIds = append(response.RemovedNodeGroupIds, id)
			}
		}
		slices.Sort(response.RemovedNodeGroupIds)
		if len(response.Groups) == 0 && len(response.RemovedNodeGroupIds) == 0 && proto.Equal(network, c.sentNetwork) {
			return nil
		}
	}
	if full {
		response.BuildInfo = &dshackle.BuildInfo{Version: buildinfo.ProductVersion()}
		if c.announced {
			c.nextFull = now.Add(resync)
		} else {
			c.nextFull = now.Add(time.Duration(rand.Int64N(int64(max(resync, 1)))) + 1)
		}
	}
	c.announced = true
	c.sentGroups = current
	c.sentNetwork = network
	return response
}

// Stable repeated-field order makes equality independent of Go map iteration.
// Only group messages are normalized; the legacy chain stream stays untouched.
func canonicalGroupEvents(events []*dshackle.ChainEvent) {
	for _, event := range events {
		if methods := event.GetSupportedMethodsEvent(); methods != nil {
			slices.Sort(methods.Methods)
		}
		if subs := event.GetSupportedSubscriptionsEvent(); subs != nil {
			slices.Sort(subs.Subs)
		}
		if bounds := event.GetLowerBoundsEvent(); bounds != nil {
			slices.SortFunc(bounds.LowerBounds, func(a, b *dshackle.LowerBound) int { return cmp.Compare(a.LowerBoundType, b.LowerBoundType) })
		}
		if blocks := event.GetFinalizationDataEvent(); blocks != nil {
			slices.SortFunc(blocks.FinalizationData, func(a, b *dshackle.FinalizationData) int { return cmp.Compare(a.Type, b.Type) })
		}
		if nodes := event.GetNodesEvent(); nodes != nil {
			for _, node := range nodes.Nodes {
				slices.SortFunc(node.Labels, func(a, b *dshackle.Label) int { return cmp.Compare(a.Name, b.Name) })
			}
			slices.SortFunc(nodes.Nodes, func(a, b *dshackle.NodeDetails) int {
				return slices.CompareFunc(a.Labels, b.Labels, func(a, b *dshackle.Label) int {
					if c := cmp.Compare(a.Name, b.Name); c != 0 {
						return c
					}
					return cmp.Compare(a.Value, b.Value)
				})
			})
		}
	}
}
