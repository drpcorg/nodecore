package health_server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpointAlwaysOk(t *testing.T) {
	server := NewHealthServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReadyEndpointReturnsUnavailableWithoutAvailableChains(t *testing.T) {
	server := NewHealthServer(&healthSupervisorStub{})
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestReadyEndpointReturnsOkWhenAnyChainAvailable(t *testing.T) {
	server := NewHealthServer(&healthSupervisorStub{chains: []upstreams.ChainSupervisor{
		&healthChainSupervisorStub{chain: chains.ETHEREUM, status: protocol.Unavailable},
		&healthChainSupervisorStub{chain: chains.POLYGON, status: protocol.Available},
	}})
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestStatusEndpointReturnsDetailedChainStatuses(t *testing.T) {
	server := NewHealthServer(&healthSupervisorStub{chains: []upstreams.ChainSupervisor{
		&healthChainSupervisorStub{chain: chains.ETHEREUM, status: protocol.Available},
		&healthChainSupervisorStub{chain: chains.POLYGON, status: protocol.Unavailable},
	}})
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body statusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.Ready)
	assert.Equal(t, []chainStatus{
		{Chain: chains.ETHEREUM.String(), Status: protocol.Available.String()},
		{Chain: chains.POLYGON.String(), Status: protocol.Unavailable.String()},
	}, body.Chains)
}

type healthSupervisorStub struct {
	chains []upstreams.ChainSupervisor
}

func (h *healthSupervisorStub) GetChainSupervisor(chain chains.Chain) upstreams.ChainSupervisor {
	return nil
}
func (h *healthSupervisorStub) GetChainSupervisors() []upstreams.ChainSupervisor { return h.chains }
func (h *healthSupervisorStub) GetUpstream(string) upstreams.Upstream            { return nil }
func (h *healthSupervisorStub) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return nil
}
func (h *healthSupervisorStub) StartUpstreams() {}
func (h *healthSupervisorStub) SubscribeChainSupervisor(name string) *utils.Subscription[upstreams.ChainSupervisorEvent] {
	return nil
}

type healthChainSupervisorStub struct {
	chain  chains.Chain
	status protocol.AvailabilityStatus
}

func (h *healthChainSupervisorStub) Start()                 {}
func (h *healthChainSupervisorStub) GetChain() chains.Chain { return h.chain }
func (h *healthChainSupervisorStub) GetChainState() upstreams.ChainSupervisorState {
	return upstreams.ChainSupervisorState{Status: h.status}
}
func (h *healthChainSupervisorStub) GetMethod(methodName string) *specs.Method { return nil }
func (h *healthChainSupervisorStub) GetMethods() []string                      { return nil }
func (h *healthChainSupervisorStub) GetUpstreamState(upstreamId string) *protocol.UpstreamState {
	return nil
}
func (h *healthChainSupervisorStub) GetSortedUpstreamIds(filterFunc upstreams.FilterUpstream, sortFunc upstreams.SortUpstream) []string {
	return nil
}
func (h *healthChainSupervisorStub) GetUpstreamIds() []string                          { return nil }
func (h *healthChainSupervisorStub) PublishUpstreamEvent(event protocol.UpstreamEvent) {}
func (h *healthChainSupervisorStub) SubscribeState(name string) *utils.Subscription[*upstreams.ChainSupervisorStateWrapperEvent] {
	return nil
}

var _ upstreams.ChainSupervisor = (*healthChainSupervisorStub)(nil)
var _ upstreams.UpstreamSupervisor = (*healthSupervisorStub)(nil)
