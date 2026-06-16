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

	mu       sync.Mutex
	cond     *sync.Cond
	inFlight int
}

func newUnaryRequestCtx() *unaryRequestCtx {
	c := &unaryRequestCtx{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *unaryRequestCtx) trackUpstreamCall() func() {
	// we have to track upstream calls since there could be parallel ones, and we need to wait for all of them to finish
	// to be able to collect all the results. A plain in-flight counter guarded by a mutex + cond is used instead of a
	// sync.WaitGroup: parallel/dispatch processors (e.g. fan-out) can start a tracked call after getResults has begun
	// waiting, which would make a WaitGroup panic ("reused before previous Wait has returned").
	c.mu.Lock()
	c.inFlight++
	c.mu.Unlock()
	once := sync.Once{}
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.inFlight--
			if c.inFlight == 0 {
				c.cond.Broadcast()
			}
			c.mu.Unlock()
		})
	}
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
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.inFlight > 0 {
		c.cond.Wait()
	}
	return slices.Clone(c.requestResults)
}

var _ requestCtx = (*unaryRequestCtx)(nil)
