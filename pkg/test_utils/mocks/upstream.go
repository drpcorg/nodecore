package mocks

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/stretchr/testify/mock"
)

type MethodsMock struct {
	mock.Mock
}

func NewMethodsMock() *MethodsMock {
	return &MethodsMock{}
}

func (m *MethodsMock) GetMethod(methodName string) *specs.Method {
	args := m.Called(methodName)
	if args.Get(0) == nil {
		return nil
	}

	return args.Get(0).(*specs.Method)
}

func (m *MethodsMock) GetSupportedMethods() mapset.Set[string] {
	args := m.Called()

	return args.Get(0).(mapset.Set[string])
}

func (m *MethodsMock) HasMethod(s string) bool {
	args := m.Called(s)

	return args.Get(0).(bool)
}

func (m *MethodsMock) IsCacheable(ctx context.Context, data any, methodName string) bool {
	args := m.Called(data, methodName)

	return args.Get(0).(bool)
}

type UpstreamSupervisorMock struct {
	mock.Mock
}

func NewUpstreamSupervisorMock() *UpstreamSupervisorMock {
	return &UpstreamSupervisorMock{}
}

func (u *UpstreamSupervisorMock) SubscribeChainSupervisor(name string) *utils.Subscription[upstreams.ChainSupervisorEvent] {
	args := u.Called(name)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*utils.Subscription[upstreams.ChainSupervisorEvent])
}

func (u *UpstreamSupervisorMock) GetChainSupervisors() []upstreams.ChainSupervisor {
	args := u.Called()

	return args.Get(0).([]upstreams.ChainSupervisor)
}

func (u *UpstreamSupervisorMock) GetChainSupervisor(chain chains.Chain) upstreams.ChainSupervisor {
	args := u.Called(chain)
	if args.Get(0) == nil {
		return nil
	}

	return args.Get(0).(upstreams.ChainSupervisor)
}

func (u *UpstreamSupervisorMock) GetUpstream(id string) upstreams.Upstream {
	args := u.Called(id)

	return args.Get(0).(*upstreams.BaseUpstream)
}

func (u *UpstreamSupervisorMock) GetExecutor() failsafe.Executor[*protocol.ResponseHolderWrapper] {
	args := u.Called()

	return args.Get(0).(failsafe.Executor[*protocol.ResponseHolderWrapper])
}

func (u *UpstreamSupervisorMock) StartUpstreams() {
	u.Called()
}
