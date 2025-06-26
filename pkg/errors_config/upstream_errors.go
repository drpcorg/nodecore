package errors_config

import (
	_ "embed"
	"fmt"
	"gopkg.in/yaml.v3"
	"regexp"
	"strings"
)

type ErrorConfig struct {
	Re  *regexp.Regexp `yaml:"re"`
	Str *string        `yaml:"str"`
}

type ErrorConfigs struct {
	RetryableErrors    []ErrorConfig `yaml:"retryable-errors"`
	NonRetryableErrors []ErrorConfig `yaml:"non-retryable-errors"`
}

//go:embed errors.yaml
var ErrorConfigsBytes []byte

var errorConfigs *ErrorConfigs

func init() {
	var errCfg ErrorConfigs
	err := yaml.Unmarshal(ErrorConfigsBytes, &errCfg)
	if err != nil {
		panic(fmt.Sprintf("couldn't load errors.yaml, cause - %s", err.Error()))
	}
	errorConfigs = &errCfg
}

func (e ErrorConfig) isMatched(error string) bool {
	if e.Re != nil {
		return e.Re.MatchString(error)
	}
	if e.Str != nil {
		return strings.Contains(error, *e.Str)
	}
	return false
}

func IsRetryable(error string) bool {
	for _, errCfg := range errorConfigs.NonRetryableErrors {
		if errCfg.isMatched(error) {
			return false
		}
	}
	for _, errCfg := range errorConfigs.RetryableErrors {
		if errCfg.isMatched(error) {
			return true
		}
	}
	return false
}
