package ratelimiter

import (
	"regexp"
	"strconv"

	"github.com/drpcorg/nodecore/internal/config"
)

type RateLimitBudget struct {
	Name   string
	Rules  []Rule
	Engine RateLimitEngine
}

func (b *RateLimitBudget) Allow(method string) (bool, error) {
	items := make([]RateLimitCommand, 0)
	for i, rule := range b.Rules {
		if rule.Check.match(method) {
			items = append(items, RateLimitCommand{
				Type: rule.Type,
				Name: b.Name + "-" + strconv.Itoa(i) + "-" + rule.Check.name(),
			})
		}
	}
	return b.Engine.Execute(items)
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
