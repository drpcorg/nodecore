package hook

import (
	"context"
	"github.com/drpcorg/nodecore/internal/protocol"
)

type HookStatsService interface {
	AddRequestResults(requestResults []protocol.RequestResult)
}

type StatsHook struct {
	statsService HookStatsService
}

func (s *StatsHook) OnResponseReceived(
	_ context.Context,
	request protocol.RequestHolder,
	_ *protocol.ResponseHolderWrapper,
) {
	go func() {
		s.statsService.AddRequestResults(request.RequestObserver().GetResults())
	}()
}

func NewStatsHook(statsService HookStatsService) *StatsHook {
	return &StatsHook{
		statsService: statsService,
	}
}

var _ protocol.ResponseReceivedHook = (*StatsHook)(nil)
