package upstreams

import (
	"errors"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/hedgepolicy"
	"github.com/rs/zerolog"
)

type ctxKey string

const RequestKey ctxKey = "request"

type NoHedgeError struct {
}

func (n NoHedgeError) Error() string {
	return "no hedge"
}

func ExecutionError(hedgeCount int, err error) error {
	if hedgeCount == 0 {
		return err
	} else {
		return NoHedgeError{}
	}
}

func createExecutor(policies ...failsafe.Policy[*protocol.ResponseHolderWrapper]) failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return failsafe.NewExecutor[*protocol.ResponseHolderWrapper](policies...)
}

func createHedgePolicy(hedgeConfig *config.HedgeConfig) failsafe.Policy[*protocol.ResponseHolderWrapper] {
	hedgePolicy := hedgepolicy.BuilderWithDelay[*protocol.ResponseHolderWrapper](hedgeConfig.Delay).
		WithMaxHedges(hedgeConfig.Count).
		OnHedge(func(event failsafe.ExecutionEvent[*protocol.ResponseHolderWrapper]) {
			ctx := event.Context()
			request := ctx.Value(RequestKey).(protocol.RequestHolder)
			if request != nil {
				zerolog.Ctx(ctx).Debug().Msgf("attemting to hedge a request %s", request.Method())
			}
		}).
		CancelIf(func(wrapper *protocol.ResponseHolderWrapper, err error) bool {
			return !errors.Is(err, NoHedgeError{})
		})

	return hedgePolicy.Build()
}
