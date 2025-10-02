package flow

import (
	"context"
	"regexp"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/samber/lo"
)

var methodPatterns = []*regexp.Regexp{
	regexp.MustCompile(`method ([A-Za-z0-9_]+) does not exist/is not available`),
	regexp.MustCompile(`([A-Za-z0-9_]+) found but the containing module is disabled`),
	regexp.MustCompile(`[Mm]ethod not found`),
	regexp.MustCompile(`The method ([A-Za-z0-9_]+) is not available`),
}

type MethodBanHook struct {
	upstreamSupervisor upstreams.UpstreamSupervisor
}

func (m *MethodBanHook) OnResponseReceived(_ context.Context, request protocol.RequestHolder, respWrapper *protocol.ResponseHolderWrapper) {
	if !respWrapper.Response.HasError() {
		return
	}

	errorMessage := respWrapper.Response.GetError().Message
	ok := lo.SomeBy(methodPatterns, func(item *regexp.Regexp) bool {
		return item.MatchString(errorMessage)
	})
	if !ok {
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

var _ ResponseReceivedHook = (*MethodBanHook)(nil)
