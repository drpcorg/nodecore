package ratelimiter

import (
	"regexp"
	"strconv"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

var rateLimitRequestMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "nodecore",
	Subsystem: "ratelimiter",
	Name:      "rate_limit_budget_requests",
	Help:      "The number of requests to the rate limit budget",
}, []string{"budget", "method"})

var rateLimitExceededMetrics = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "nodecore",
	Subsystem: "ratelimiter",
	Name:      "rate_limit_budget_exceeded",
	Help:      "The number of requests that exceeded the rate limit budget",
}, []string{"budget", "method"})

func init() {
	prometheus.MustRegister(rateLimitRequestMetrics, rateLimitExceededMetrics)
}

type RateLimitBudget struct {
	Name   string
	Rules  []Rule
	Engine RateLimitEngine
}

func (b *RateLimitBudget) Allow(method string) (bool, error) {
	rateLimitRequestMetrics.WithLabelValues(b.Name, method).Inc()
	items := make([]RateLimitCommand, 0)
	for i, rule := range b.Rules {
		if rule.Check.match(method) {
			items = append(items, RateLimitCommand{
				Type: rule.Type,
				Name: b.Name + "-" + strconv.Itoa(i) + "-" + rule.Check.name(),
			})
		}
	}
	allowed, err := b.Engine.Execute(items)
	if !allowed {
		rateLimitExceededMetrics.WithLabelValues(b.Name, method).Inc()
	}
	return allowed, err
}

func NewRateLimitBudget(config *config.RateLimitBudget, engine RateLimitEngine) *RateLimitBudget {
	rules := make([]Rule, 0)
	for _, rule := range config.Config.Rules {
		var rulecheck ruleCheck
		if rule.Method != "" {
			rulecheck = &ruleCheckMethod{
				method: rule.Method,
			}
		} else {
			rulecheck = &ruleCheckPattern{
				pattern: *regexp.MustCompile(rule.Pattern),
			}
		}
		tp := &FixedRateLimiterType{
			requests: rule.Requests,
			period:   rule.Period,
		}
		rules = append(rules, Rule{
			Type:  tp,
			Check: rulecheck,
		})
	}
	return &RateLimitBudget{
		Name:   config.Name,
		Rules:  rules,
		Engine: engine,
	}
}
