package flow

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
)

type MethodBanHook struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func (m *MethodBanHook) OnResponseReceived(_ context.Context, request protocol.RequestHolder, respWrapper *protocol.ResponseHolderWrapper) {
	if !respWrapper.Response.HasError() {
		return
	}

	// Only a definite "this method is absent" justifies a ban. MethodAvailable and
	// MethodAvailabilityUnknown both leave the method alone.
	if protocol.ClassifyMethodAvailability(respWrapper.Response.GetError()) != protocol.MethodNotAvailable {
		return
	}

	responseUpstream := m.upstreamSupervisor.GetUpstream(respWrapper.UpstreamId)
	if responseUpstream == nil {
		return
	}

	go responseUpstream.BanMethod(request.Method())
}

func NewMethodBanHook(upstreamSupervisor upstreams.UpstreamSupervisor) *MethodBanHook {
	return &MethodBanHook{upstreamSupervisor: upstreamSupervisor}
}

var _ protocol.ResponseReceivedHook = (*MethodBanHook)(nil)
