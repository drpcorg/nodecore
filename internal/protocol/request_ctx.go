package protocol

import (
	"slices"
	"sync"
)

type requestCtx interface {
	trackUpstreamCall() func()
	addResult(result RequestResult)
	addFinalResult(result RequestResult)
	getResults() []RequestResult
}

type subscriptionRequestCtx struct {
	// TODO: add subscription results
}

func (s *subscriptionRequestCtx) trackUpstreamCall() func() {
	// no need to track upstream calls since there is only one call
	return func() {}
}

func (s *subscriptionRequestCtx) addResult(result RequestResult) {
}

func (s *subscriptionRequestCtx) addFinalResult(result RequestResult) {
	s.addResult(result)
}

func (s *subscriptionRequestCtx) getResults() []RequestResult {
	return nil
}

func newSubscriptionRequestCtx() *subscriptionRequestCtx {
	return &subscriptionRequestCtx{}
}

var _ requestCtx = (*subscriptionRequestCtx)(nil)

type unaryRequestCtx struct {
	requestResults []RequestResult

	mu sync.RWMutex
	wg sync.WaitGroup
}

func newUnaryRequestCtx() *unaryRequestCtx {
	return &unaryRequestCtx{}
}

func (c *unaryRequestCtx) trackUpstreamCall() func() {
	// we have to track upstream calls since there could be parallel ones, and we need to wait for all of them to finish
	// to be able to collect all the results
	c.wg.Add(1)
	once := sync.Once{}
	return func() { once.Do(c.wg.Done) }
}

func (c *unaryRequestCtx) addResult(result RequestResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestResults = append(c.requestResults, result)
}

func (c *unaryRequestCtx) addFinalResult(result RequestResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requestResults) == 0 {
		// it means that we haven't added this result before
		c.requestResults = append(c.requestResults, result)
	}
}

func (c *unaryRequestCtx) getResults() []RequestResult {
	c.wg.Wait()

	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.requestResults)
}

var _ requestCtx = (*unaryRequestCtx)(nil)
