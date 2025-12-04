package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/nodecore/pkg/chains"
)

func (u *UpstreamConfig) validate(rateLimitBudgetNames mapset.Set[string], torProxyUrl string) error {
	if err := u.ScorePolicyConfig.validate(); err != nil {
		return fmt.Errorf("error during score policy config validation, cause: %s", err.Error())
	}

	for chain, chainDefault := range u.ChainDefaults {
		if !chains.IsSupported(chain) {
			return fmt.Errorf("error during chain defaults validation, cause: not supported chain %s", chain)
		}
		if err := chainDefault.validate(); err != nil {
			return fmt.Errorf("error during chain '%s' defaults validation, cause: %s", chain, err.Error())
		}
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return fmt.Errorf("error during failsafe validation of upstream-conifg: %s", err.Error())
	}

	if len(u.Upstreams) == 0 {
		return errors.New("there must be at least one upstream in the config")
	}

	idSet := mapset.NewThreadUnsafeSet[string]()
	for i, upstream := range u.Upstreams {
		if upstream.Id == "" {
			return fmt.Errorf("error during upstream validation, cause: no upstream id under index %d", i)
		}
		if idSet.Contains(upstream.Id) {
			return fmt.Errorf("error during upstream validation, cause: upstream with id '%s' already exists", upstream.Id)
		}
		if err := upstream.validate(torProxyUrl); err != nil {
			return fmt.Errorf("error during upstream '%s' validation, cause: %s", upstream.Id, err.Error())
		}
		// Validate rate limit budget reference
		if upstream.RateLimitBudget != "" && !rateLimitBudgetNames.Contains(upstream.RateLimitBudget) {
			return fmt.Errorf("upstream '%s' references non-existent rate limit budget '%s'", upstream.Id, upstream.RateLimitBudget)
		}
		if upstream.RateLimitAutoTune != nil {
			if err := upstream.RateLimitAutoTune.validate(); err != nil {
				return fmt.Errorf("error during rate limit auto-tune config validation, cause: %s", err.Error())
			}
		}
		idSet.Add(upstream.Id)
	}

	return nil
}

func (s *ScorePolicyConfig) validate() error {
	if s.CalculationInterval <= 0 {
		return errors.New("the calculation interval can't be less than 0")
	}
	if s.CalculationFunctionName != "" && s.CalculationFunctionFilePath != "" {
		return errors.New("one setting must be specified - either 'calculation-function' or 'calculation-function-file-path'")
	}
	if s.CalculationFunctionName != "" {
		_, ok := defaultRatingFunctions[s.CalculationFunctionName]
		if !ok {
			return fmt.Errorf("'%s' default function doesn't exist", s.CalculationFunctionName)
		}
	}
	_, err := s.compileFunc()
	if err != nil {
		return fmt.Errorf("couldn't read a ts script, %s", err.Error())
	}
	return nil
}

func (f *FailsafeConfig) validate() error {
	if f.HedgeConfig != nil {
		if err := f.HedgeConfig.validate(); err != nil {
			return fmt.Errorf("hedge config validation error - %s", err.Error())
		}
	}
	if f.RetryConfig != nil {
		if err := f.RetryConfig.validate(); err != nil {
			return fmt.Errorf("retry config validation error - %s", err.Error())
		}
	}
	return nil
}

func (o *UpstreamOptions) validate() error {
	if o.InternalTimeout < 0 {
		return errors.New("internal timeout can't be less than 0")
	}
	if o.ValidationInterval < 0 {
		return errors.New("validation interval can't be less than 0")
	}
	return nil
}

func (r *RetryConfig) validate() error {
	if r.Attempts < 1 {
		return errors.New("the number of attempts can't be less than 1")
	}
	if r.Delay <= 0 {
		return errors.New("the retry delay can't be less than 0")
	}
	if r.MaxDelay != nil && *r.MaxDelay <= 0 {
		return errors.New("the retry max delay can't be less than 0")
	}
	if r.Jitter != nil && *r.Jitter <= 0 {
		return errors.New("the retry jitter can't be 0")
	}
	if r.MaxDelay != nil && r.Delay > *r.MaxDelay {
		return errors.New("the retry delay can't be greater than the retry max delay")
	}
	return nil
}

func (h *HedgeConfig) validate() error {
	if h.Count <= 0 {
		return errors.New("the number of hedges can't be less than 1")
	}
	if h.Delay.Milliseconds() < 50 {
		return errors.New("the hedge delay can't be less than 50ms")
	}
	return nil
}

func (u *Upstream) validate(torProxyUrl string) error {
	if !chains.IsSupported(u.ChainName) {
		return fmt.Errorf("not supported chain '%s'", u.ChainName)
	}

	if len(u.Connectors) == 0 {
		return fmt.Errorf("there must be at least one upstream connector")
	}

	if u.RateLimit != nil {
		if err := u.RateLimit.validate(); err != nil {
			return fmt.Errorf("error during rate limit validation, cause: %s", err.Error())
		}
	}

	connectorTypeSet := mapset.NewThreadUnsafeSet[ApiConnectorType]()
	for _, connector := range u.Connectors {
		if connectorTypeSet.Contains(connector.Type) {
			return fmt.Errorf("there can be only one connector of type '%s'", connector.Type)
		}
		if err := connector.validate(torProxyUrl); err != nil {
			return err
		}
		connectorTypeSet.Add(connector.Type)
	}

	if err := u.HeadConnector.validate(); err != nil {
		return fmt.Errorf("invalid head connector - '%s'", u.HeadConnector)
	}

	if !connectorTypeSet.Contains(u.HeadConnector) {
		return fmt.Errorf("there is no '%s' connector for head", u.HeadConnector)
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return err
	}

	if err := u.Methods.validate(); err != nil {
		return err
	}

	if err := u.Options.validate(); err != nil {
		return err
	}

	return nil
}

func (m *MethodsConfig) validate() error {
	if m.BanDuration <= 0 {
		return errors.New("the method ban duration can't be less than 0")
	}

	enabled := mapset.NewThreadUnsafeSet[string]()

	for _, enabledMethod := range m.EnableMethods {
		enabled.Add(enabledMethod)
	}

	for _, disabledMethod := range m.DisableMethods {
		if enabled.Contains(disabledMethod) {
			return fmt.Errorf("the method '%s' must not be enabled and disabled at the same time", disabledMethod)
		}
	}

	return nil
}

func (c *ChainDefaults) validate() error {
	if c.Options != nil {
		if err := c.Options.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *ApiConnectorConfig) validate(torProxyUrl string) error {
	if err := c.Type.validate(); err != nil {
		return err
	}

	if c.Url == "" {
		return fmt.Errorf("url must be specified for connector '%s'", c.Type)
	}
	parsedUrl, err := url.Parse(c.Url)
	if err != nil {
		return fmt.Errorf("invalid url for connector '%s' - %s", c.Type, err.Error())
	}
	if parsedUrl.Scheme == "" || parsedUrl.Host == "" {
		return fmt.Errorf("invalid url for connector '%s' - scheme and host are required", c.Type)
	}
	if strings.HasSuffix(parsedUrl.Hostname(), ".onion") {
		if torProxyUrl == "" {
			return errors.New("tor proxy url is required for onion endpoints")
		}
	}

	return nil
}

func (t ApiConnectorType) validate() error {
	switch t {
	case Grpc, JsonRpc, Rest, Ws:
	default:
		return fmt.Errorf("invalid connector type - '%s'", t)
	}
	return nil
}
