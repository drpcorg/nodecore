package emerald

import (
	"cmp"
	"slices"

	"github.com/drpcorg/nodecore/internal/buildinfo"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
)

// DescribeChains builds the answer to the unary Describe call: the build
// version and one entry per served chain, read from each chain supervisor's
// current state. It is a point-in-time read; live state travels over
// SubscribeChainStatus.
//
// Unlike that stream, a chain is listed before its first head arrives (with
// height 0). Chains, methods and subscriptions are sorted, since the sources
// they are read from iterate in random order.
func DescribeChains(upstreamSupervisor upstreams.UpstreamSupervisor) *dshackle.DescribeResponse {
	described := make([]*dshackle.DescribeChain, 0)

	for _, chainSupervisor := range upstreamSupervisor.GetChainSupervisors() {
		grpcId := chains.GetChain(chainSupervisor.GetChain().String()).GrpcId
		// without a grpc id the chain has no ChainRef, so a client could
		// neither tell it apart nor call it
		if grpcId == 0 {
			continue
		}
		described = append(described, describeChain(dshackle.ChainRef(grpcId), chainSupervisor.GetChainState()))
	}

	slices.SortFunc(described, func(a, b *dshackle.DescribeChain) int {
		return cmp.Compare(a.GetChain(), b.GetChain())
	})

	return &dshackle.DescribeResponse{
		Chains: described,
		BuildInfo: &dshackle.BuildInfo{
			Version: buildinfo.ProductVersion(),
		},
	}
}

func describeChain(chainRef dshackle.ChainRef, state upstreams.ChainSupervisorState) *dshackle.DescribeChain {
	supportedMethods := state.Methods.GetSupportedMethods().ToSlice()
	slices.Sort(supportedMethods)
	supportedSubscriptions := state.SubMethods.ToSlice()
	slices.Sort(supportedSubscriptions)

	return &dshackle.DescribeChain{
		Chain: chainRef,
		Status: &dshackle.ChainStatus{
			Chain:        chainRef,
			Availability: availabilityStatusToApi(state.Status),
		},
		Nodes:                  nodeDetailsToApi(state.ChainLabels),
		SupportedMethods:       supportedMethods,
		SupportedSubscriptions: supportedSubscriptions,
		CurrentHeight:          int64(state.HeadData.Head.Height),
	}
}
