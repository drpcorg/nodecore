//go:build nodegroups_load

package emerald_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/server/emerald"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/public/pkg/dshackle"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type groupLoadServer struct {
	dshackle.UnimplementedBlockchainServer
	supervisor upstreams.UpstreamSupervisor
}

func (s *groupLoadServer) SubscribeNodeGroupStatus(request *dshackle.SubscribeNodeGroupStatusRequest, stream dshackle.Blockchain_SubscribeNodeGroupStatusServer) error {
	return emerald.SubscribeNodeGroupStatusWithResync(s.supervisor, request, stream, 25*time.Millisecond, 24*time.Hour)
}

// Local TCP load probe: real gRPC, production status producer, eight independent
// consumers and atomic upstream snapshots. It excludes native calls and auth.
// CPU/allocation measurements include both server AND clients in this process.
func TestNodeGroupStatusLoad(t *testing.T) {
	loadMethodSpecs(t)
	chain := newUpstreamsChainSupervisor(chains.ETHEREUM)
	conf := &config.MethodsConfig{}
	for i := 0; i < 200; i++ {
		conf.EnableMethods = append(conf.EnableMethods, fmt.Sprintf("bench_method_%03d", i))
	}
	var networkMethods methods.Methods
	for i := 0; i < 128; i++ {
		m, err := methods.NewUpstreamMethods("eth", conf, []specs.ApiConnectorType{specs.JsonRpcConnector})
		require.NoError(t, err)
		networkMethods = m
		state := protocol.DefaultUpstreamState(m, mapset.NewThreadUnsafeSet[protocol.Cap](), fmt.Sprintf("%06d", i), nil, nil)
		state.HeadData = protocol.NewBlockWithHeight(1000)
		state.Labels.AddLabel("client_type", "geth")
		chain.set(fmt.Sprintf("node-%03d", i), state.WithNodeGroupCache())
	}
	network := newChainState(chains.ETHEREUM, protocol.NewBlockWithHeight(1000), nil)
	network.Methods = networkMethods
	network.ChainLabels = []upstreams.AggregatedLabels{upstreams.NewAggregatedLabels(128, map[string]string{"client_type": "geth"})}
	chain.SetState(network)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	dshackle.RegisterBlockchainServer(server, &groupLoadServer{supervisor: newChainsUpstreamSupervisor(chain)})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	client := dshackle.NewBlockchainClient(conn)
	var ready, received, wire atomic.Int64
	var latest [8]atomic.Uint64
	var issued sync.Map
	var latencyMu sync.Mutex
	var latencies []time.Duration
	var consumers sync.WaitGroup
	defer func() { cancel(); consumers.Wait() }()
	for subscriber := 0; subscriber < 8; subscriber++ {
		request := &dshackle.SubscribeNodeGroupStatusRequest{FullSeparation: true}
		if os.Getenv("NODE_GROUP_LOAD_COMPACT") == "1" {
			field := request.ProtoReflect().Descriptor().Fields().ByName("compact_updates")
			require.NotNil(t, field, "compact mode requires the new API")
			request.ProtoReflect().Set(field, protoreflect.ValueOfBool(true))
		}
		stream, err := client.SubscribeNodeGroupStatus(ctx, request)
		require.NoError(t, err)
		consumers.Go(func() {
			heads := make(map[string]uint64)
			first := true
			for {
				response, err := stream.Recv()
				if err != nil {
					if ctx.Err() == nil {
						t.Errorf("load stream: %v", err)
					}
					return
				}
				for _, group := range response.Groups {
					heads[group.NodeGroupId] = group.GetHead().GetHeight()
				}
				if len(heads) != 128 {
					t.Errorf("incomplete catalog: %d", len(heads))
					return
				}
				low := ^uint64(0)
				for _, height := range heads {
					low = min(low, height)
				}
				latest[subscriber].Store(low)
				if first {
					first = false
					ready.Add(1)
					continue
				}
				received.Add(1)
				wire.Add(int64(proto.Size(response)))
				if start, ok := issued.Load(low); ok {
					latencyMu.Lock()
					latencies = append(latencies, time.Since(start.(time.Time)))
					latencyMu.Unlock()
				}
			}
		})
	}
	require.Eventually(t, func() bool { return ready.Load() == 8 }, 5*time.Second, 10*time.Millisecond)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var cpuBefore, cpuAfter syscall.Rusage
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuBefore))
	start := time.Now()
	ticker := time.NewTicker(25 * time.Millisecond)
	// Five seconds of 40Hz all-node head bursts; skipped/coalesced snapshots are
	// allowed, but every consumer must converge after the producer stops.
	for i := 0; i < 200; i++ {
		<-ticker.C
		height := uint64(1001 + i)
		issued.Store(height, time.Now())
		chain.mu.Lock()
		for id, previous := range chain.states {
			next := *previous
			next.HeadData = protocol.NewBlockWithHeight(height)
			chain.states[id] = &next
		}
		chain.mu.Unlock()
		network.HeadData = upstreams.NewChainHeadData(protocol.NewBlockWithHeight(height), "")
		chain.SetState(network)
		chain.changed.Notify()
	}
	ticker.Stop()
	require.Eventually(t, func() bool {
		for i := range latest {
			if latest[i].Load() != 1200 {
				return false
			}
		}
		return true
	}, 3*time.Second, 10*time.Millisecond)
	elapsed := time.Since(start)
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &cpuAfter))
	runtime.ReadMemStats(&after)
	cancel()
	consumers.Wait()
	slices.Sort(latencies)
	require.NotEmpty(t, latencies)
	cpu := time.Duration(cpuAfter.Utime.Nano() + cpuAfter.Stime.Nano() - cpuBefore.Utime.Nano() - cpuBefore.Stime.Nano())
	t.Logf("nodes=128 subscribers=8 methods=%d issued=200 received=%d elapsed=%s process_cpu=%s alloc_MiB=%.1f payload_MiB=%.1f p50=%s p95=%s max=%s", networkMethods.GetSupportedMethods().Cardinality(), received.Load(), elapsed, cpu, float64(after.TotalAlloc-before.TotalAlloc)/(1<<20), float64(wire.Load())/(1<<20), latencies[len(latencies)/2], latencies[len(latencies)*95/100], latencies[len(latencies)-1])
}
