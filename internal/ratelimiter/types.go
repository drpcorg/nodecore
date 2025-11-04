package ratelimiter

import (
	"regexp"
	"time"
)

type RateLimiterType interface {
	tp() bool
}

type FixedRateLimiterType struct {
	requests int
	period   time.Duration
}

func NewFixedRateLimiterType(requests int, period time.Duration) *FixedRateLimiterType {
	return &FixedRateLimiterType{
		requests: requests,
		period:   period,
	}
}

func (r *FixedRateLimiterType) tp() bool {
	return true
}

type RateLimitEngine interface {
	Execute([]RateLimitCommand) (bool, error)
}

type RateLimitCommand struct {
	Type RateLimiterType
	Name string
}

type ruleCheck interface {
	match(method string) bool
	name() string
}

type ruleCheckMethod struct {
	method string
}

func (r *ruleCheckMethod) match(method string) bool {
	return r.method == method
}
func (r *ruleCheckMethod) name() string {
	return r.method
}

type ruleCheckPattern struct {
	pattern regexp.Regexp
}

func (r *ruleCheckPattern) match(method string) bool {
	return r.pattern.MatchString(method)
}

func (r *ruleCheckPattern) name() string {
	return r.pattern.String()
}

type Rule struct {
	Check ruleCheck
	Type  RateLimiterType
}
