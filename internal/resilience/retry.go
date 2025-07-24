package resilience

import (
	"errors"
	"fmt"
	"github.com/failsafe-go/failsafe-go/common"
	"math/rand"
	"slices"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/policy"
)

const defaultMaxRetries = 2

// ErrExceeded is a convenience error sentinel that can be used to build policies that handle ExceededError, such as via
// HandleErrors(retrypolicy.ErrExceeded). It can also be used with Errors.Is to determine whether an error is a
// retrypolicy.ExceededError.
var ErrExceeded = errors.New("retries exceeded")

// ExceededError is returned when a RetryPolicy's max attempts or max duration are exceeded. This type can be used with
// HandleErrorTypes(retrypolicy.ExceededError{}).
type ExceededError struct {
	LastResult any
	LastError  error
}

func (e ExceededError) Error() string {
	return fmt.Sprintf("retries exceeded. last result: %v, last error: %v", e.LastResult, e.LastError)
}

func (e ExceededError) Is(err error) bool {
	if err == ErrExceeded {
		return true
	}
	return err == e
}

func (e ExceededError) Unwrap() error {
	if e.LastError != nil {
		return e.LastError
	}
	return fmt.Errorf("failure: %v", e.LastResult)
}

type RetryPolicy[R any] interface {
	failsafe.Policy[R]
}

type RetryPolicyBuilder[R any] interface {
	failsafe.FailurePolicyBuilder[RetryPolicyBuilder[R], R]
	failsafe.DelayablePolicyBuilder[RetryPolicyBuilder[R], R]

	ReturnPreviousResultOnErrors(errs ...error) RetryPolicyBuilder[R]

	// AbortOnResult specifies that retries should be aborted if the execution result matches the result using
	// reflect.DeepEqual.
	AbortOnResult(result R) RetryPolicyBuilder[R]

	// AbortOnErrors specifies that retries should be aborted if the execution error matches any of the errs using errors.Is.
	AbortOnErrors(errs ...error) RetryPolicyBuilder[R]

	// AbortOnErrorTypes specifies the errors whose types should cause retries to be aborted. Any execution errors or their
	// Unwrapped parents whose type matches any of the errs' types will cause to be aborted. This is similar to the check
	// that errors.As performs.
	AbortOnErrorTypes(errs ...any) RetryPolicyBuilder[R]

	// AbortIf specifies that retries should be aborted if the predicate matches the result or error.
	AbortIf(predicate func(R, error) bool) RetryPolicyBuilder[R]

	// ReturnLastFailure configures the policy to return the last failure result or error after attempts are exceeded,
	// rather than returning ExceededError.
	ReturnLastFailure() RetryPolicyBuilder[R]

	// WithMaxAttempts sets the max number of execution attempts to perform. -1 indicates no limit. This method has the same
	// effect as setting 1 more than WithMaxRetries. For example, 2 retries equal 3 attempts.
	WithMaxAttempts(maxAttempts int) RetryPolicyBuilder[R]

	// WithMaxRetries sets the max number of retries to perform when an execution attempt fails. -1 indicates no limit. This
	// method has the same effect as setting 1 less than WithMaxAttempts. For example, 2 retries equal 3 attempts.
	WithMaxRetries(maxRetries int) RetryPolicyBuilder[R]

	// WithMaxDuration sets the max duration to perform retries for, else the execution will be failed.
	WithMaxDuration(maxDuration time.Duration) RetryPolicyBuilder[R]

	// WithBackoff wets the delay between retries, exponentially backing off to the maxDelay and multiplying consecutive
	// delays by a factor of 2. Replaces any previously configured fixed or random delays.
	WithBackoff(delay time.Duration, maxDelay time.Duration) RetryPolicyBuilder[R]

	// WithBackoffFactor sets the delay between retries, exponentially backing off to the maxDelay and multiplying
	// consecutive delays by the delayFactor. Replaces any previously configured fixed or random delays.
	WithBackoffFactor(delay time.Duration, maxDelay time.Duration, delayFactor float32) RetryPolicyBuilder[R]

	// WithRandomDelay sets a random delay between the delayMin and delayMax (inclusive) to occur between retries.
	// Replaces any previously configured delay or backoff delay.
	WithRandomDelay(delayMin time.Duration, delayMax time.Duration) RetryPolicyBuilder[R]

	// WithJitter sets the jitter to randomly vary retry delays by. For each retry delay, a random portion of the jitter will
	// be added or subtracted to the delay. For example: a jitter of 100 milliseconds will randomly add between -100 and 100
	// milliseconds to each retry delay. Replaces any previously configured jitter factor.
	//
	// Jitter should be combined with fixed, random, or exponential backoff delays. If no delays are configured, this setting
	// is ignored.
	WithJitter(jitter time.Duration) RetryPolicyBuilder[R]

	// WithJitterFactor sets the jitterFactor to randomly vary retry delays by. For each retry delay, a random portion of the
	// delay multiplied by the jitterFactor will be added or subtracted to the delay. For example: a retry delay of 100
	// milliseconds and a jitterFactor of .25 will result in a random retry delay between 75 and 125 milliseconds. Replaces
	// any previously configured jitter duration.
	//
	// Jitter should be combined with fixed, random, or exponential backoff delays. If no delays are configured, this setting
	// is ignored.
	WithJitterFactor(jitterFactor float32) RetryPolicyBuilder[R]

	// OnAbort registers the listener to be called when an execution is aborted.
	OnAbort(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R]

	// OnRetryScheduled registers the listener to be called when a retry is about to be scheduled. This method differs from
	// OnRetry since it is called when a retry is initially scheduled but before any configured delay, whereas OnRetry is
	// called after a delay, just before the retry attempt takes place.
	OnRetryScheduled(listener func(failsafe.ExecutionScheduledEvent[R])) RetryPolicyBuilder[R]

	// OnRetry registers the listener to be called when a retry is about to be attempted.
	OnRetry(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R]

	// OnRetriesExceeded registers the listener to be called when an execution fails and the max retry attempts or max
	// duration are exceeded. The provided event will contain the last execution result and error.
	OnRetriesExceeded(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R]

	// Build returns a new RetryPolicy using the builder's configuration.
	Build() RetryPolicy[R]
}

type retryCfg[R any] struct {
	*policy.BaseFailurePolicy[R]
	*policy.BaseDelayablePolicy[R]
	*policy.BaseAbortablePolicy[R]

	returnLastFailure          bool
	delayMin                   time.Duration
	delayMax                   time.Duration
	delayFactor                float32
	maxDelay                   time.Duration
	jitter                     time.Duration
	jitterFactor               float32
	maxDuration                time.Duration
	maxRetries                 int
	returnPreviousResultErrors []error

	onAbort           func(failsafe.ExecutionEvent[R])
	onRetry           func(failsafe.ExecutionEvent[R])
	onRetryScheduled  func(failsafe.ExecutionScheduledEvent[R])
	onRetriesExceeded func(failsafe.ExecutionEvent[R])
}

var _ RetryPolicyBuilder[any] = &retryCfg[any]{}

type retryPolicy[R any] struct {
	*retryCfg[R]
}

// WithDefaults creates a RetryPolicy for execution result type R that allows 3 execution attempts max with no delay. To
// configure additional options on a RetryPolicy, use Builder instead.
func WithDefaults[R any]() RetryPolicy[R] {
	return Builder[R]().Build()
}

// Builder creates a RetryPolicyBuilder for execution result type R, which by default will build a RetryPolicy that
// allows 3 execution attempts max with no delay, unless configured otherwise.
func Builder[R any]() RetryPolicyBuilder[R] {
	return &retryCfg[R]{
		BaseFailurePolicy:          &policy.BaseFailurePolicy[R]{},
		BaseDelayablePolicy:        &policy.BaseDelayablePolicy[R]{},
		BaseAbortablePolicy:        &policy.BaseAbortablePolicy[R]{},
		maxRetries:                 defaultMaxRetries,
		returnPreviousResultErrors: make([]error, 0),
	}
}

func (c *retryCfg[R]) Build() RetryPolicy[R] {
	rpCopy := *c
	return &retryPolicy[R]{
		retryCfg: &rpCopy, // TODO copy base fields
	}
}

func (c *retryCfg[R]) AbortOnResult(result R) RetryPolicyBuilder[R] {
	c.BaseAbortablePolicy.AbortOnResult(result)
	return c
}

func (c *retryCfg[R]) AbortOnErrors(errs ...error) RetryPolicyBuilder[R] {
	c.BaseAbortablePolicy.AbortOnErrors(errs...)
	return c
}

func (c *retryCfg[R]) AbortOnErrorTypes(errs ...any) RetryPolicyBuilder[R] {
	c.BaseAbortablePolicy.AbortOnErrorTypes(errs...)
	return c
}

func (c *retryCfg[R]) AbortIf(predicate func(R, error) bool) RetryPolicyBuilder[R] {
	c.BaseAbortablePolicy.AbortIf(predicate)
	return c
}

func (c *retryCfg[R]) HandleErrors(errs ...error) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.HandleErrors(errs...)
	return c
}

func (c *retryCfg[R]) HandleErrorTypes(errs ...any) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.HandleErrorTypes(errs...)
	return c
}

func (c *retryCfg[R]) HandleResult(result R) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.HandleResult(result)
	return c
}

func (c *retryCfg[R]) HandleIf(predicate func(R, error) bool) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.HandleIf(predicate)
	return c
}

func (c *retryCfg[R]) ReturnLastFailure() RetryPolicyBuilder[R] {
	c.returnLastFailure = true
	return c
}

func (c *retryCfg[R]) WithMaxAttempts(maxAttempts int) RetryPolicyBuilder[R] {
	if maxAttempts == -1 {
		c.maxRetries = -1
	} else {
		c.maxRetries = maxAttempts - 1
	}
	return c
}

func (c *retryCfg[R]) WithMaxRetries(maxRetries int) RetryPolicyBuilder[R] {
	c.maxRetries = maxRetries
	return c
}

func (c *retryCfg[R]) WithMaxDuration(maxDuration time.Duration) RetryPolicyBuilder[R] {
	c.maxDuration = maxDuration
	return c
}

func (c *retryCfg[R]) WithDelay(delay time.Duration) RetryPolicyBuilder[R] {
	c.BaseDelayablePolicy.WithDelay(delay)
	return c
}

func (c *retryCfg[R]) WithDelayFunc(delayFunc failsafe.DelayFunc[R]) RetryPolicyBuilder[R] {
	c.BaseDelayablePolicy.WithDelayFunc(delayFunc)
	return c
}

func (c *retryCfg[R]) WithBackoff(delay time.Duration, maxDelay time.Duration) RetryPolicyBuilder[R] {
	return c.WithBackoffFactor(delay, maxDelay, 2)
}

func (c *retryCfg[R]) WithBackoffFactor(delay time.Duration, maxDelay time.Duration, delayFactor float32) RetryPolicyBuilder[R] {
	c.BaseDelayablePolicy.WithDelay(delay)
	c.maxDelay = maxDelay
	c.delayFactor = delayFactor

	// Clear random delay
	c.delayMin = 0
	c.delayMax = 0
	return c
}

func (c *retryCfg[R]) WithRandomDelay(delayMin time.Duration, delayMax time.Duration) RetryPolicyBuilder[R] {
	c.delayMin = delayMin
	c.delayMax = delayMax

	// Clear non-random delay
	c.Delay = 0
	c.maxDelay = 0
	return c
}

func (c *retryCfg[R]) WithJitter(jitter time.Duration) RetryPolicyBuilder[R] {
	c.jitter = jitter
	return c
}

func (c *retryCfg[R]) ReturnPreviousResultOnErrors(errs ...error) RetryPolicyBuilder[R] {
	c.returnPreviousResultErrors = errs
	return c
}

func (c *retryCfg[R]) WithJitterFactor(jitterFactor float32) RetryPolicyBuilder[R] {
	c.jitterFactor = jitterFactor
	return c
}

func (c *retryCfg[R]) OnSuccess(listener func(event failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.OnSuccess(listener)
	return c
}

func (c *retryCfg[R]) OnFailure(listener func(event failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R] {
	c.BaseFailurePolicy.OnFailure(listener)
	return c
}

func (c *retryCfg[R]) OnAbort(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R] {
	c.onAbort = listener
	return c
}

func (c *retryCfg[R]) OnRetry(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R] {
	c.onRetry = listener
	return c
}

func (c *retryCfg[R]) OnRetryScheduled(listener func(failsafe.ExecutionScheduledEvent[R])) RetryPolicyBuilder[R] {
	c.onRetryScheduled = listener
	return c
}

func (c *retryCfg[R]) OnRetriesExceeded(listener func(failsafe.ExecutionEvent[R])) RetryPolicyBuilder[R] {
	c.onRetriesExceeded = listener
	return c
}

func (c *retryCfg[R]) allowsRetries() bool {
	return c.maxRetries == -1 || c.maxRetries > 0
}

func (rp *retryPolicy[R]) ToExecutor(_ R) any {
	rpe := &retryExecutor[R]{
		BaseExecutor: &policy.BaseExecutor[R]{
			BaseFailurePolicy: rp.BaseFailurePolicy,
		},
		retryPolicy: rp,
	}
	rpe.Executor = rpe
	return rpe
}

type retryExecutor[R any] struct {
	*policy.BaseExecutor[R]
	*retryPolicy[R]

	// Mutable state
	failedAttempts  int
	retriesExceeded bool
	lastDelay       time.Duration // The last backoff delay time
}

var _ policy.Executor[any] = &retryExecutor[any]{}

func (e *retryExecutor[R]) Apply(innerFn func(failsafe.Execution[R]) *common.PolicyResult[R]) func(failsafe.Execution[R]) *common.PolicyResult[R] {
	return func(exec failsafe.Execution[R]) *common.PolicyResult[R] {
		execInternal := exec.(policy.ExecutionInternal[R])

		var prevResult *common.PolicyResult[R]
		var result *common.PolicyResult[R]
		for {
			prevResult = result
			result = innerFn(exec)
			if canceled, cancelResult := execInternal.IsCanceledWithResult(); canceled {
				return cancelResult
			}
			if e.retriesExceeded {
				return result
			}

			result = e.PostExecute(execInternal, result)
			if result.Done && slices.Contains(e.returnPreviousResultErrors, result.Error) && prevResult != nil {
				return &common.PolicyResult[R]{
					Result:     prevResult.Result,
					Error:      prevResult.Error,
					Done:       true,
					Success:    true,
					SuccessAll: true,
				}
			} else if result.Done {
				return result
			}

			// Record result
			if cancelResult := execInternal.RecordResult(result); cancelResult != nil {
				return cancelResult
			}

			// Delay
			delay := e.getDelay(exec)
			if e.onRetryScheduled != nil {
				e.onRetryScheduled(failsafe.ExecutionScheduledEvent[R]{
					ExecutionAttempt: execInternal.CopyWithResult(result),
					Delay:            delay,
				})
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-exec.Canceled():
				timer.Stop()
			}

			// Prepare for next iteration
			if cancelResult := execInternal.InitializeRetry(); cancelResult != nil {
				return cancelResult
			}

			// Call retry listener
			if e.onRetry != nil {
				e.onRetry(failsafe.ExecutionEvent[R]{ExecutionAttempt: execInternal.CopyWithResult(result)})
			}
		}
	}
}

// OnFailure updates failedAttempts and retriesExceeded, and calls event listeners
func (e *retryExecutor[R]) OnFailure(exec policy.ExecutionInternal[R], result *common.PolicyResult[R]) *common.PolicyResult[R] {
	e.BaseExecutor.OnFailure(exec, result)

	e.failedAttempts++
	maxRetriesExceeded := e.maxRetries != -1 && e.failedAttempts > e.maxRetries
	maxDurationExceeded := e.maxDuration != 0 && exec.ElapsedTime() > e.maxDuration
	e.retriesExceeded = maxRetriesExceeded || maxDurationExceeded
	isAbortable := e.IsAbortable(result.Result, result.Error)
	shouldRetry := !isAbortable && !e.retriesExceeded && e.allowsRetries()
	done := isAbortable || !shouldRetry

	// Call listeners
	if isAbortable && e.onAbort != nil {
		e.onAbort(failsafe.ExecutionEvent[R]{ExecutionAttempt: exec.CopyWithResult(result)})
	}
	if e.retriesExceeded {
		if !isAbortable && e.onRetriesExceeded != nil {
			e.onRetriesExceeded(failsafe.ExecutionEvent[R]{ExecutionAttempt: exec.CopyWithResult(result)})
		}
		if !e.returnLastFailure {
			return failureResult[R](ExceededError{
				LastResult: result.Result,
				LastError:  result.Error,
			})
		}
	}
	return result.WithDone(done, false)
}

// getDelay updates lastDelay and returns the new delay
func (e *retryExecutor[R]) getDelay(exec failsafe.ExecutionAttempt[R]) time.Duration {
	var delay time.Duration
	if computedDelay := e.ComputeDelay(exec); computedDelay != -1 {
		delay = computedDelay
	} else {
		delay = e.getFixedOrRandomDelay(exec)
	}
	if delay != 0 {
		delay = e.adjustForJitter(delay)
	}
	delay = e.adjustForMaxDuration(delay, exec.ElapsedTime())
	return delay
}

func (e *retryExecutor[R]) getFixedOrRandomDelay(exec failsafe.ExecutionAttempt[R]) time.Duration {
	if e.Delay != 0 {
		// Adjust for backoffs
		if e.lastDelay != 0 && exec.Retries() >= 1 && e.maxDelay != 0 {
			backoffDelay := time.Duration(float32(e.lastDelay) * e.delayFactor)
			e.lastDelay = min(backoffDelay, e.maxDelay)
		} else {
			e.lastDelay = e.Delay
		}
		return e.lastDelay
	}
	if e.delayMin != 0 && e.delayMax != 0 {
		return time.Duration(randomDelayInRange(e.delayMin.Nanoseconds(), e.delayMax.Nanoseconds(), rand.Float64()))
	}
	return 0
}

func (e *retryExecutor[R]) adjustForJitter(delay time.Duration) time.Duration {
	if e.jitter != 0 {
		delay = randomDelay(delay, e.jitter, rand.Float64())
	} else if e.jitterFactor != 0 {
		delay = randomDelayFactor(delay, e.jitterFactor, rand.Float32())
	}
	return delay
}

func (e *retryExecutor[R]) adjustForMaxDuration(delay time.Duration, elapsed time.Duration) time.Duration {
	if e.maxDuration != 0 {
		delay = min(delay, e.maxDuration-elapsed)
	}
	return max(0, delay)
}
