package resilience

import (
	"errors"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/rs/zerolog"
)

type ctxKey string

const RequestKey ctxKey = "request"

func CreateFlowExecutor(policies ...failsafe.Policy[*protocol.ResponseHolderWrapper]) failsafe.Executor[*protocol.ResponseHolderWrapper] {
	return failsafe.NewExecutor[*protocol.ResponseHolderWrapper](policies...)
}

func CreateFlowRetryPolicy(retryConfig *config.RetryConfig) failsafe.Policy[*protocol.ResponseHolderWrapper] {
	retry := Builder[*protocol.ResponseHolderWrapper]()

	if retryConfig.Attempts > 0 {
		retry.WithMaxAttempts(retryConfig.Attempts)
	}
	if retryConfig.Delay > 0 {
		if retryConfig.MaxDelay != nil && *retryConfig.MaxDelay > 0 {
			retry.WithBackoff(retryConfig.Delay, *retryConfig.MaxDelay)
		} else {
			retry.WithDelay(retryConfig.Delay)
		}
	}
	if retryConfig.Jitter != nil && *retryConfig.Jitter > 0 {
		retry.WithJitter(*retryConfig.Jitter)
	}

	retry.HandleIf(func(wrapper *protocol.ResponseHolderWrapper, err error) bool {
		return !errors.Is(err, protocol.StopRetryErr{}) && wrapper != nil && protocol.IsRetryable(wrapper.Response)
	})

	retry.ReturnPreviousResultOnErrors(protocol.StopRetryErr{})

	retry.OnRetry(func(event failsafe.ExecutionEvent[*protocol.ResponseHolderWrapper]) {
		ctx := event.Context()
		request := ctx.Value(RequestKey).(protocol.RequestHolder)
		if request != nil {
			wrapper := event.LastResult()
			zerolog.Ctx(ctx).
				Debug().
				Err(wrapper.Response.GetError()).
				Msgf("attemting to retry a request %s, failed upstream is %s", request.Method(), wrapper.UpstreamId)
		}
	})

	retry.ReturnLastFailure()

	return retry.Build()
}

func CreateFlowParallelHedgePolicy(hedgeConfig *config.HedgeConfig) failsafe.Policy[*protocol.ResponseHolderWrapper] {
	hedge := BuilderWithDelay[*protocol.ResponseHolderWrapper](hedgeConfig.Delay).
		WithMaxHedges(hedgeConfig.Count).
		OnHedge(func(event failsafe.ExecutionEvent[*protocol.ResponseHolderWrapper]) {
			ctx := event.Context()
			requestByKey := ctx.Value(RequestKey)
			if requestByKey != nil {
				if request, ok := requestByKey.(protocol.RequestHolder); ok {
					zerolog.Ctx(ctx).Debug().Msgf("attemting to hedge a request %s", request.Method())
				}
			}
		}).
		ResultTypeFunc(protocol.GetResponseType).
		CancelIf(func(wrapper *protocol.ResponseHolderWrapper, err error) bool {
			return wrapper != nil && !wrapper.Response.HasError()
		})

	return hedge.Build()
}

func CreateUpstreamExecutor(policies ...failsafe.Policy[protocol.ResponseHolder]) failsafe.Executor[protocol.ResponseHolder] {
	return failsafe.NewExecutor[protocol.ResponseHolder](policies...)
}

func CreateUpstreamRetryPolicy(retryConfig *config.RetryConfig) failsafe.Policy[protocol.ResponseHolder] {
	retry := retrypolicy.Builder[protocol.ResponseHolder]()

	if retryConfig.Attempts > 0 {
		retry.WithMaxAttempts(retryConfig.Attempts)
	}
	if retryConfig.Delay > 0 {
		if retryConfig.MaxDelay != nil && *retryConfig.MaxDelay > 0 {
			retry.WithBackoff(retryConfig.Delay, *retryConfig.MaxDelay)
		} else {
			retry.WithDelay(retryConfig.Delay)
		}
	}
	if retryConfig.Jitter != nil && *retryConfig.Jitter > 0 {
		retry.WithJitter(*retryConfig.Jitter)
	}

	retry.HandleIf(func(response protocol.ResponseHolder, err error) bool {
		return protocol.IsRetryable(response)
	})

	retry.OnRetry(func(event failsafe.ExecutionEvent[protocol.ResponseHolder]) {
		ctx := event.Context()
		request := ctx.Value(RequestKey).(protocol.RequestHolder)
		if request != nil {
			response := event.LastResult()
			zerolog.Ctx(ctx).Debug().Err(response.GetError()).Msgf("attemting to retry a request %s", request.Method())
		}
	})

	retry.ReturnLastFailure()

	return retry.Build()
}
