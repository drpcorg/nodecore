package stats

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type StatsHook struct {
	statsService StatsService
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

func NewStatsHook(statsService StatsService) *StatsHook {
	return &StatsHook{
		statsService: statsService,
	}
}

var _ protocol.ResponseReceivedHook = (*StatsHook)(nil)
