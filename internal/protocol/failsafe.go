package protocol

import (
	"errors"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/hedgepolicy"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
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

func CreateFlowExecutor(policies ...failsafe.Policy[*ResponseHolderWrapper]) failsafe.Executor[*ResponseHolderWrapper] {
	return failsafe.NewExecutor[*ResponseHolderWrapper](policies...)
}

func CreateFlowHedgePolicy(hedgeConfig *config.HedgeConfig) failsafe.Policy[*ResponseHolderWrapper] {
	hedgePolicy := hedgepolicy.BuilderWithDelay[*ResponseHolderWrapper](hedgeConfig.Delay).
		WithMaxHedges(hedgeConfig.Count).
		OnHedge(func(event failsafe.ExecutionEvent[*ResponseHolderWrapper]) {
			ctx := event.Context()
			requestByKey := ctx.Value(RequestKey)
			if requestByKey != nil {
				if request, ok := requestByKey.(RequestHolder); ok {
					zerolog.Ctx(ctx).Debug().Msgf("attemting to hedge a request %s", request.Method())
				}
			}
		}).
		CancelIf(func(wrapper *ResponseHolderWrapper, err error) bool {
			return !errors.Is(err, NoHedgeError{})
		})

	return hedgePolicy.Build()
}

func CreateUpstreamExecutor(policies ...failsafe.Policy[ResponseHolder]) failsafe.Executor[ResponseHolder] {
	return failsafe.NewExecutor[ResponseHolder](policies...)
}

func CreateUpstreamRetryPolicy(retryConfig *config.RetryConfig) failsafe.Policy[ResponseHolder] {
	retryPolicy := retrypolicy.Builder[ResponseHolder]()

	if retryConfig.Attempts > 0 {
		retryPolicy.WithMaxAttempts(retryConfig.Attempts)
	}
	if retryConfig.Delay > 0 {
		if retryConfig.MaxDelay > 0 {
			retryPolicy.WithBackoff(retryConfig.Delay, retryConfig.MaxDelay)
		} else {
			retryPolicy.WithDelay(retryConfig.Delay)
		}
	}
	if retryConfig.Jitter > 0 {
		retryPolicy.WithJitter(retryConfig.Jitter)
	}

	retryPolicy.HandleIf(func(response ResponseHolder, err error) bool {
		return IsRetryable(response)
	})

	retryPolicy.OnRetry(func(event failsafe.ExecutionEvent[ResponseHolder]) {
		ctx := event.Context()
		request := ctx.Value(RequestKey).(RequestHolder)
		if request != nil {
			response := event.LastResult()
			zerolog.Ctx(ctx).Debug().Err(response.GetError()).Msgf("attemting to retry a request %s", request.Method())
		}
	})

	retryPolicy.ReturnLastFailure()

	return retryPolicy.Build()
}
