package resilience

import (
	"sync/atomic"
	"time"

	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/common"
	"github.com/failsafe-go/failsafe-go/policy"
	"github.com/samber/lo"
)

// ParallelHedgePolicy is a copy of failsafe.ParallelHedgePolicy but that allows to execute all hedges requests in parallel at the same time
// rather than waiting for a specified delay between executions that can influence the overall latency
type ParallelHedgePolicy[R any] interface {
	failsafe.Policy[R]
}

type ParallelHedgePolicyBuilder[R any] interface {
	CancelOnResult(result R) ParallelHedgePolicyBuilder[R]

	CancelOnErrors(errs ...error) ParallelHedgePolicyBuilder[R]

	CancelOnErrorTypes(errs ...any) ParallelHedgePolicyBuilder[R]

	CancelIf(predicate func(R, error) bool) ParallelHedgePolicyBuilder[R]

	OnHedge(listener func(failsafe.ExecutionEvent[R])) ParallelHedgePolicyBuilder[R]

	WithIgnoredErrors(errs ...error) ParallelHedgePolicyBuilder[R]

	// ResultTypeFunc is used to determine the ResultType of an inner function's response
	// then these values are used to sort the accumulated responses and choose the best one
	ResultTypeFunc(f func(R, error) protocol.ResultType) ParallelHedgePolicyBuilder[R]

	WithMaxHedges(maxHedges int) ParallelHedgePolicyBuilder[R]

	Build() ParallelHedgePolicy[R]
}

type parallelHedgePolicyConfig[R any] struct {
	*policy.BaseAbortablePolicy[R]

	delayFunc failsafe.DelayFunc[R]
	maxHedges int
	onHedge   func(failsafe.ExecutionEvent[R])

	ignoredErrors  []error
	resultTypeFunc func(R, error) protocol.ResultType
}

var _ ParallelHedgePolicyBuilder[any] = &parallelHedgePolicyConfig[any]{}

func WithDelay[R any](delay time.Duration) ParallelHedgePolicy[R] {
	return BuilderWithDelay[R](delay).Build()
}

func WithDelayFunc[R any](delayFunc failsafe.DelayFunc[R]) ParallelHedgePolicy[R] {
	return BuilderWithDelayFunc[R](delayFunc).Build()
}

func BuilderWithDelay[R any](delay time.Duration) ParallelHedgePolicyBuilder[R] {
	return BuilderWithDelayFunc[R](func(exec failsafe.ExecutionAttempt[R]) time.Duration {
		return delay
	})
}

func BuilderWithDelayFunc[R any](delayFunc failsafe.DelayFunc[R]) ParallelHedgePolicyBuilder[R] {
	defaultFunc := func(r R, err error) protocol.ResultType {
		return protocol.ResultOk
	}
	return &parallelHedgePolicyConfig[R]{
		BaseAbortablePolicy: &policy.BaseAbortablePolicy[R]{},
		delayFunc:           delayFunc,
		maxHedges:           1,
		ignoredErrors:       make([]error, 0),
		resultTypeFunc:      defaultFunc,
	}
}

type parallelHedgePolicy[R any] struct {
	*parallelHedgePolicyConfig[R]
}

var _ ParallelHedgePolicy[any] = &parallelHedgePolicy[any]{}

func (c *parallelHedgePolicyConfig[R]) CancelOnResult(result R) ParallelHedgePolicyBuilder[R] {
	c.AbortOnResult(result)
	return c
}

func (c *parallelHedgePolicyConfig[R]) CancelOnErrors(errs ...error) ParallelHedgePolicyBuilder[R] {
	c.AbortOnErrors(errs...)
	return c
}

func (c *parallelHedgePolicyConfig[R]) CancelOnErrorTypes(errs ...any) ParallelHedgePolicyBuilder[R] {
	c.AbortOnErrorTypes(errs...)
	return c
}

func (c *parallelHedgePolicyConfig[R]) CancelIf(predicate func(R, error) bool) ParallelHedgePolicyBuilder[R] {
	c.AbortIf(predicate)
	return c
}

func (c *parallelHedgePolicyConfig[R]) OnHedge(listener func(failsafe.ExecutionEvent[R])) ParallelHedgePolicyBuilder[R] {
	c.onHedge = listener
	return c
}

func (c *parallelHedgePolicyConfig[R]) WithMaxHedges(maxHedges int) ParallelHedgePolicyBuilder[R] {
	c.maxHedges = maxHedges
	return c
}

func (c *parallelHedgePolicyConfig[R]) WithIgnoredErrors(errs ...error) ParallelHedgePolicyBuilder[R] {
	c.ignoredErrors = errs
	return c
}

func (c *parallelHedgePolicyConfig[R]) ResultTypeFunc(f func(R, error) protocol.ResultType) ParallelHedgePolicyBuilder[R] {
	c.resultTypeFunc = f
	return c
}

func (c *parallelHedgePolicyConfig[R]) Build() ParallelHedgePolicy[R] {
	hCopy := *c
	if !c.IsConfigured() {
		c.AbortIf(func(r R, err error) bool {
			return true
		})
	}
	return &parallelHedgePolicy[R]{
		parallelHedgePolicyConfig: &hCopy,
	}
}

func (h *parallelHedgePolicy[R]) ToExecutor(_ R) any {
	he := &hedgeExecutor[R]{
		BaseExecutor:        &policy.BaseExecutor[R]{},
		parallelHedgePolicy: h,
	}
	he.Executor = he
	return he
}

type hedgeExecutor[R any] struct {
	*policy.BaseExecutor[R]
	*parallelHedgePolicy[R]
}

var _ policy.Executor[any] = &hedgeExecutor[any]{}

func (e *hedgeExecutor[R]) Apply(innerFn func(failsafe.Execution[R]) *common.PolicyResult[R]) func(failsafe.Execution[R]) *common.PolicyResult[R] {
	return func(exec failsafe.Execution[R]) *common.PolicyResult[R] {
		type execResult struct {
			result     *common.PolicyResult[R]
			index      int
			resultType protocol.ResultType
		}
		parentExecution := exec.(policy.ExecutionInternal[R])
		executions := make([]policy.ExecutionInternal[R], e.maxHedges+1)

		resultSent := atomic.Bool{}
		resultCount := atomic.Int32{}
		resultChan := make(chan execResult, 1) // Only one result is sent

		possibleResults := utils.CMap[int, execResult]{}

		executions[0] = parentExecution.CopyForCancellable().(policy.ExecutionInternal[R])

		executeFunc := func(hedgeExec policy.ExecutionInternal[R], execIdx int) {
			now := time.Now()
			result := innerFn(hedgeExec)
			since := time.Since(now)

			isFirstError := execIdx == 0 && result.Error != nil
			isFinalResult := int(resultCount.Add(1)) == e.maxHedges+1
			isCancellable := e.IsAbortable(result.Result, result.Error) || isFirstError
			noNeedToHedge := execIdx == 0 && since < e.delayFunc(exec)

			if (isCancellable || noNeedToHedge) && resultSent.CompareAndSwap(false, true) {
				resultChan <- execResult{result, execIdx, protocol.ResultOk}
			} else {
				possibleResults.Store(execIdx, &execResult{result, execIdx, e.resultTypeFunc(result.Result, result.Error)})
				if isFinalResult {
					possibleResultsArray := make([]*execResult, 0)
					possibleResults.Range(func(key int, val *execResult) bool {
						possibleResultsArray = append(possibleResultsArray, val)
						return true
					})

					finalResult := lo.MinBy(possibleResultsArray, func(a *execResult, b *execResult) bool {
						return a.resultType < b.resultType
					})

					if resultSent.CompareAndSwap(false, true) {
						resultChan <- *finalResult
					}
				}
			}
		}
		go executeFunc(executions[0], 0)

		var result execResult
		timer := time.NewTimer(e.delayFunc(exec))
		for {
			select {
			case <-timer.C:
				for i := 1; i < e.maxHedges+1; i++ {
					executions[i] = parentExecution.CopyForHedge().(policy.ExecutionInternal[R])
					if e.onHedge != nil {
						e.onHedge(failsafe.ExecutionEvent[R]{ExecutionAttempt: executions[i].CopyWithResult(nil)})
					}
					go executeFunc(executions[i], i)
				}
			case result = <-resultChan:
				timer.Stop()
				if canceled, cancelResult := parentExecution.IsCanceledWithResult(); canceled {
					return cancelResult
				}

				for i, execution := range executions {
					if i != result.index && execution != nil {
						execution.Cancel(nil)
					}
				}
				return result.result
			}
		}
	}
}
