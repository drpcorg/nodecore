package emerald

import (
	"fmt"
	"slices"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/specs_utils"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"google.golang.org/protobuf/proto"
)

// The fixture replaces only supervisor reads. Grouping, merge functions, key
// caching, response diffing and protobuf serialization are production code.
type groupBenchSupervisor struct {
	upstreams.ChainSupervisor
	ids     []string
	states  map[string]*protocol.UpstreamState
	network upstreams.ChainSupervisorState
}

func (s *groupBenchSupervisor) GetChain() chains.Chain   { return chains.ETHEREUM }
func (s *groupBenchSupervisor) GetUpstreamIds() []string { return slices.Clone(s.ids) }
func (s *groupBenchSupervisor) GetUpstreamState(id string) *protocol.UpstreamState {
	return s.states[id]
}
func (s *groupBenchSupervisor) GetChainState() upstreams.ChainSupervisorState { return s.network }
func groupBenchFixture(b testing.TB, nodes, groups, extraMethods int) *groupBenchSupervisor {
	b.Helper()
	specs_utils.LoadMethodSpecs()
	conf := &config.MethodsConfig{}
	for i := 0; i < extraMethods; i++ {
		conf.EnableMethods = append(conf.EnableMethods, fmt.Sprintf("bench_method_%03d", i))
	}
	s := &groupBenchSupervisor{states: make(map[string]*protocol.UpstreamState)}
	for i := 0; i < nodes; i++ {
		// Separate method objects model real upstreams, even when their sets agree.
		m, err := methods.NewUpstreamMethods("eth", conf, []specs.ApiConnectorType{specs.JsonRpcConnector})
		if err != nil {
			b.Fatal(err)
		}
		state := protocol.DefaultUpstreamState(m, mapset.NewThreadUnsafeSet[protocol.Cap](), fmt.Sprintf("%06d", i), nil, nil)
		state.Labels.AddLabel("client_type", fmt.Sprintf("client-%d", i%groups))
		state.HeadData = protocol.NewBlockWithHeight(1000)
		id := fmt.Sprintf("node-%03d", i)
		s.ids = append(s.ids, id)
		s.states[id] = state.WithNodeGroupCache()
		protocol.NodeGroupID(id, s.states[id], false)
	}
	snapshots := upstreams.NodeGroups(s, false)
	for _, g := range snapshots {
		s.network = g.State
		break
	}
	s.network.ChainLabels = nil
	for _, g := range snapshots {
		s.network.ChainLabels = append(s.network.ChainLabels, g.State.ChainLabels...)
	}
	return s
}

// One operation is one changed upstream head delivered to every subscriber.
// Full measures resync; Head measures a steady-state delta after the initial full.
// wire-B/op is serialized protobuf payload, excluding HTTP/2/TLS framing.
func BenchmarkNodeGroupStatus(b *testing.B) {
	for _, size := range []struct{ nodes, groups, methods int }{{8, 2, 0}, {32, 4, 200}, {128, 8, 200}} {
		for _, singleton := range []bool{false, true} {
			for _, subscribers := range []int{1, 8} {
				for _, full := range []bool{false, true} {
					mode := "Head"
					if full {
						mode = "Full"
					}
					b.Run(fmt.Sprintf("N%d/G%d/Extra%d/Singleton%t/S%d/%s", size.nodes, size.groups, size.methods, singleton, subscribers, mode), func(b *testing.B) {
						supervisor := groupBenchFixture(b, size.nodes, size.groups, size.methods)
						now := time.Now()
						streams := make([]*upstreamStatusChain, subscribers)
						for i := range streams {
							streams[i] = &upstreamStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
							streams[i].groupResponse(supervisor, now, time.Hour, singleton)
							streams[i].nextFull = now.Add(time.Hour)
						}
						var wire int64
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							id := supervisor.ids[i%len(supervisor.ids)]
							state := *supervisor.states[id]
							state.HeadData = protocol.NewBlockWithHeight(uint64(1001 + i))
							supervisor.states[id] = &state
							supervisor.network.HeadData = upstreams.NewChainHeadData(state.HeadData, id)
							for _, stream := range streams {
								if full {
									stream.nextFull = now
								}
								response := stream.groupResponse(supervisor, now, time.Hour, singleton)
								payload, err := proto.Marshal(response)
								if err != nil {
									b.Fatal(err)
								}
								wire += int64(len(payload))
							}
						}
						b.ReportMetric(float64(wire)/float64(b.N), "wire-B/op")
					})
				}
			}
		}
	}
}

func BenchmarkNodeGroupColdSubscriber(b *testing.B) {
	for _, singleton := range []bool{false, true} {
		b.Run(fmt.Sprintf("N128/Extra200/Singleton%t", singleton), func(b *testing.B) {
			supervisor := groupBenchFixture(b, 128, 8, 200)
			now := time.Now()
			var wire int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream := &upstreamStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
				response := stream.groupResponse(supervisor, now, time.Hour, singleton)
				payload, err := proto.Marshal(response)
				if err != nil {
					b.Fatal(err)
				}
				wire += int64(len(payload))
			}
			b.ReportMetric(float64(wire)/float64(b.N), "wire-B/op")
		})
	}
}

// A coalesced block update can advance every upstream, not just one. This is
// the upper payload case for a delta and must be measured separately.
func BenchmarkNodeGroupHeadBurst(b *testing.B) {
	for _, singleton := range []bool{false, true} {
		b.Run(fmt.Sprintf("N128/Extra200/Singleton%t/S8", singleton), func(b *testing.B) {
			supervisor := groupBenchFixture(b, 128, 8, 200)
			now := time.Now()
			streams := make([]*upstreamStatusChain, 8)
			for i := range streams {
				streams[i] = &upstreamStatusChain{ref: dshackle.ChainRef_CHAIN_ETHEREUM__MAINNET}
				streams[i].groupResponse(supervisor, now, time.Hour, singleton)
				streams[i].nextFull = now.Add(time.Hour)
			}
			var wire int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for id, previous := range supervisor.states {
					next := *previous
					next.HeadData = protocol.NewBlockWithHeight(uint64(1001 + i))
					supervisor.states[id] = &next
				}
				supervisor.network.HeadData = upstreams.NewChainHeadData(protocol.NewBlockWithHeight(uint64(1001+i)), "")
				for _, stream := range streams {
					response := stream.groupResponse(supervisor, now, time.Hour, singleton)
					payload, err := proto.Marshal(response)
					if err != nil {
						b.Fatal(err)
					}
					wire += int64(len(payload))
				}
			}
			b.ReportMetric(float64(wire)/float64(b.N), "wire-B/op")
		})
	}
}
