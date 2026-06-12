package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/methods"
	"github.com/evanw/esbuild/pkg/api"
	"github.com/samber/lo"
)

type UpstreamConfig struct {
	Upstreams         []*Upstream               `yaml:"upstreams"`
	ChainDefaults     map[string]*ChainDefaults `yaml:"chain-defaults"`
	FailsafeConfig    *FailsafeConfig           `yaml:"failsafe-config"`
	ScorePolicyConfig *ScorePolicyConfig        `yaml:"score-policy-config"`
	IntegrityConfig   *IntegrityConfig          `yaml:"integrity"`
	LabelBalancing    *LabelBalancingConfig     `yaml:"label-balancing"`
	Mode              UpstreamMode              `yaml:"mode"`
}

// LabelBalancingFor resolves the effective label-balancing config for a chain:
// a per-chain override under chain-defaults wins over the global default; if
// neither is set it returns nil (rating-only balancing).
func (u *UpstreamConfig) LabelBalancingFor(chain string) *LabelBalancingConfig {
	if chainDefaults, ok := u.ChainDefaults[chain]; ok && chainDefaults.LabelBalancing != nil {
		return chainDefaults.LabelBalancing
	}
	return u.LabelBalancing
}

type UpstreamMode string

const (
	DefaultMode UpstreamMode = "default"
	StrictMode  UpstreamMode = "strict"
)

func (u UpstreamMode) Validate() error {
	switch u {
	case DefaultMode, StrictMode:
	default:
		return fmt.Errorf("invalid upstream mode: %s", u)
	}
	return nil
}

func (u *UpstreamConfig) GetDispatchOptions(chainName string) DispatchOptions {
	defaultEnabled := u != nil && u.Mode == StrictMode
	options := DispatchOptions{
		Broadcast:    lo.ToPtr(defaultEnabled),
		MaximumValue: lo.ToPtr(defaultEnabled),
		NotNull:      lo.ToPtr(defaultEnabled),
	}
	if u == nil || u.ChainDefaults == nil {
		return options
	}
	defaults := u.ChainDefaults[chainName]
	if defaults == nil || defaults.Dispatch == nil {
		return options
	}
	if defaults.Dispatch.Broadcast != nil {
		options.Broadcast = defaults.Dispatch.Broadcast
	}
	if defaults.Dispatch.MaximumValue != nil {
		options.MaximumValue = defaults.Dispatch.MaximumValue
	}
	if defaults.Dispatch.NotNull != nil {
		options.NotNull = defaults.Dispatch.NotNull
	}
	return options
}

type Upstream struct {
	Id                string                   `yaml:"id"`
	ChainName         string                   `yaml:"chain"`
	Connectors        []*ApiConnectorConfig    `yaml:"connectors"`
	HeadConnector     string                   `yaml:"head-connector"`
	PollInterval      time.Duration            `yaml:"poll-interval"`
	Methods           *MethodsConfig           `yaml:"methods"`
	FailsafeConfig    *FailsafeConfig          `yaml:"failsafe-config"`
	Options           *chains.Options          `yaml:"options"`
	RateLimitBudget   string                   `yaml:"rate-limit-budget"`
	RateLimit         *RateLimiterConfig       `yaml:"rate-limit"`
	RateLimitAutoTune *RateLimitAutoTuneConfig `yaml:"rate-limit-auto-tune"`
	GroupLabels       []string                 `yaml:"group-labels"`
}

func (u *Upstream) GetApiConnectorTypes() []specs.ApiConnectorType {
	return lo.Map(u.Connectors, func(item *ApiConnectorConfig, index int) specs.ApiConnectorType {
		return item.GetApiConnectorType()
	})
}

func (u *Upstream) GetHeadApiConnectorType() specs.ApiConnectorType {
	return specs.GetApiConnectorType(u.HeadConnector)
}

type sortConnectorFunc func([]*ApiConnectorConfig) specs.ApiConnectorType

var sortConnectorsFunc = map[UpstreamMode]sortConnectorFunc{
	DefaultMode: func(configs []*ApiConnectorConfig) specs.ApiConnectorType {
		return lo.MinBy(configs, func(a *ApiConnectorConfig, b *ApiConnectorConfig) bool {
			return a.GetApiConnectorType() < b.GetApiConnectorType()
		}).GetApiConnectorType()
	},
	StrictMode: func(configs []*ApiConnectorConfig) specs.ApiConnectorType {
		return lo.MaxBy(configs, func(a *ApiConnectorConfig, b *ApiConnectorConfig) bool {
			return a.GetApiConnectorType() > b.GetApiConnectorType()
		}).GetApiConnectorType()
	},
}

func (u *Upstream) GetBestConnector(upstreamMode UpstreamMode) specs.ApiConnectorType {
	filteredConnectors := lo.Filter(u.Connectors, func(item *ApiConnectorConfig, index int) bool {
		return item.GetApiConnectorType() != specs.UnknownType && !specs.IsAdditionalApiConnectorType(item.GetApiConnectorType())
	})

	if len(filteredConnectors) > 0 {
		if sortFunc, ok := sortConnectorsFunc[upstreamMode]; ok {
			return sortFunc(filteredConnectors)
		}
	}
	return specs.UnknownType
}

type ChainDefaults struct {
	PollInterval   time.Duration         `yaml:"poll-interval"`
	Options        *chains.Options       `yaml:"options"`
	LabelBalancing *LabelBalancingConfig `yaml:"label-balancing"`
	Dispatch       *DispatchOptions      `yaml:"dispatch"`
}

// LabelBalancingConfig enables priority-group balancing: upstreams tagged with
// group-labels are served in the configured Order, falling through to the next
// group when the current one can't serve. Within a group the usual rating order
// applies. See docs/nodecore/05-upstream-config.md.
type LabelBalancingConfig struct {
	// Order is the ordered list of group label names, highest priority first.
	Order []string `yaml:"order"`
	// PassOnError, when true, jumps to the next group on a retryable error even
	// if the current group still has untried upstreams. When false (default), a
	// retryable error retries within the current group and only advances once
	// the current group has no selectable upstream left.
	PassOnError bool `yaml:"pass-on-error"`
	// IncludeDefault, when true (the default), routes upstreams that carry none
	// of the Order labels as a final fallback group tried after all configured
	// groups. When false those upstreams are excluded while label-balancing is
	// active.
	IncludeDefault *bool `yaml:"include-default"`
}

func (l *LabelBalancingConfig) validate() error {
	if len(l.Order) == 0 {
		return errors.New("label-balancing order must contain at least one label")
	}
	seen := mapset.NewThreadUnsafeSet[string]()
	for _, label := range l.Order {
		if label == "" {
			return errors.New("label-balancing order must not contain an empty label")
		}
		if seen.Contains(label) {
			return fmt.Errorf("label-balancing order contains a duplicate label '%s'", label)
		}
		seen.Add(label)
	}
	return nil
}

type DispatchOptions struct {
	Broadcast    *bool `yaml:"broadcast"`
	MaximumValue *bool `yaml:"maximum-value"`
	NotNull      *bool `yaml:"not-null"`
}

type FailsafeConfig struct {
	HedgeConfig   *HedgeConfig   `yaml:"hedge"`
	TimeoutConfig *TimeoutConfig `yaml:"timeout"`
	RetryConfig   *RetryConfig   `yaml:"retry"`
}

type ScorePolicyConfig struct {
	CalculationInterval         time.Duration `yaml:"calculation-interval"`
	CalculationFunctionName     string        `yaml:"calculation-function-name"`      // a func name from a 'defaultRatingFunctions' map
	CalculationFunctionFilePath string        `yaml:"calculation-function-file-path"` // a path to the file with a function

	calculationFunc goja.Callable
}

type IntegrityConfig struct {
	Enabled bool `yaml:"enabled"`
}

type ApiConnectorConfig struct {
	Type    string            `yaml:"type"`
	Url     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Ca      string            `yaml:"ca"`
	// ResponseHeaderDeny lists upstream response-header names that must
	// NOT be forwarded to the client, in addition to the connector's
	// hard-coded defaults (hop-by-hop per RFC 7230 plus Set-Cookie /
	// Server). Matching is case-insensitive (HTTP header rules).
	ResponseHeaderDeny []string `yaml:"response-header-deny,omitempty"`
}

func (a *ApiConnectorConfig) GetApiConnectorType() specs.ApiConnectorType {
	return specs.GetApiConnectorType(a.Type)
}

var registry = new(require.Registry)

func (s *ScorePolicyConfig) GetScoreFunc() (goja.Callable, error) {
	if s.calculationFunc == nil {
		sortUpstreams, err := s.compileFunc()
		if err != nil {
			panic(err)
		}
		s.calculationFunc = sortUpstreams
	}
	return s.calculationFunc, nil
}

func (s *ScorePolicyConfig) compileFunc() (goja.Callable, error) {
	var tsFunc string
	if s.CalculationFunctionName != "" {
		tsFunc = defaultRatingFunctions[s.CalculationFunctionName]
	} else {
		funcBytes, err := os.ReadFile(s.CalculationFunctionFilePath)
		if err != nil {
			return nil, err
		}
		tsFunc = string(funcBytes)
	}

	result := api.Transform(tsFunc, api.TransformOptions{
		Loader: api.LoaderTS,
	})
	if len(result.Errors) > 0 {
		errorsText := lo.Map(result.Errors, func(item api.Message, index int) string {
			return item.Text
		})
		return nil, errors.New(strings.Join(errorsText, "; "))
	}

	vm := goja.New()
	_, err := vm.RunString(string(result.Code))
	if err != nil {
		return nil, err
	}
	registry.Enable(vm)
	console.Enable(vm)

	valueFunc := vm.Get("sortUpstreams")
	if valueFunc == nil {
		return nil, errors.New(`no sortUpstreams() function in the specified script`)
	}
	sortUpstreams, ok := goja.AssertFunction(valueFunc)
	if !ok {
		return nil, errors.New("sortUpstreams is not a function")
	}
	return sortUpstreams, nil
}

type RetryConfig struct {
	Attempts int            `yaml:"attempts"`
	Delay    time.Duration  `yaml:"delay"`
	MaxDelay *time.Duration `yaml:"max-delay"`
	Jitter   *time.Duration `yaml:"jitter"`
}

type HedgeConfig struct { // works only on the execution flow level
	Delay time.Duration `yaml:"delay"`
	Count int           `yaml:"max"`
}

type TimeoutConfig struct {
	Timeout time.Duration `yaml:"duration"`
}

type MethodsConfig struct {
	BanDuration    time.Duration `yaml:"ban-duration"`
	EnableMethods  []string      `yaml:"enable"`
	DisableMethods []string      `yaml:"disable"`
}

func (u *UpstreamConfig) validate(rateLimitBudgetNames mapset.Set[string], torProxyUrl string) error {
	if err := u.Mode.Validate(); err != nil {
		return err
	}
	if err := u.ScorePolicyConfig.validate(); err != nil {
		return fmt.Errorf("error during score policy config validation, cause: %s", err.Error())
	}

	if u.LabelBalancing != nil {
		if err := u.LabelBalancing.validate(); err != nil {
			return fmt.Errorf("error during label-balancing config validation, cause: %s", err.Error())
		}
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

func (r *RetryConfig) validate() error {
	if r.Attempts < 1 {
		return errors.New("the number of attempts can't be less than 1")
	}
	if r.Delay < 0 {
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

	onlyAdditional := len(u.Connectors) == 1 && specs.IsAdditionalApiConnectorType(u.Connectors[0].GetApiConnectorType())
	if onlyAdditional {
		return fmt.Errorf(
			"additional api connector %s can't be the only upstream connector, there must be at least one plain connector %s",
			u.Connectors[0].GetApiConnectorType(),
			specs.GetPlainApiConnectorType(),
		)
	}

	connectorTypeSet := mapset.NewThreadUnsafeSet[specs.ApiConnectorType]()
	for _, connector := range u.Connectors {
		if connectorTypeSet.Contains(connector.GetApiConnectorType()) {
			return fmt.Errorf("there can be only one connector of type '%s'", connector.Type)
		}
		if err := connector.validate(torProxyUrl); err != nil {
			return err
		}
		connectorTypeSet.Add(connector.GetApiConnectorType())
	}

	if err := u.validateHeadConnector(); err != nil {
		return err
	}

	if !connectorTypeSet.Contains(u.GetHeadApiConnectorType()) {
		return fmt.Errorf("there is no '%s' connector for head", u.HeadConnector)
	}

	if err := u.FailsafeConfig.validate(); err != nil {
		return err
	}

	if err := u.Methods.validate(); err != nil {
		return err
	}

	if err := u.Options.Validate(); err != nil {
		return err
	}

	for _, label := range u.GroupLabels {
		if label == "" {
			return errors.New("group-labels must not contain an empty label")
		}
	}

	return nil
}

func (u *Upstream) validateHeadConnector() error {
	if err := specs.ValidateApiConnectorType(u.HeadConnector); err != nil {
		return fmt.Errorf("invalid head connector, %s", err.Error())
	}
	if specs.IsAdditionalApiConnectorType(u.GetHeadApiConnectorType()) {
		return fmt.Errorf("additional api connector type '%s' is forbidden for head connector", u.GetHeadApiConnectorType())
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
		if err := c.Options.Validate(); err != nil {
			return err
		}
	}
	if c.LabelBalancing != nil {
		if err := c.LabelBalancing.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (a *ApiConnectorConfig) validate(torProxyUrl string) error {
	if err := specs.ValidateApiConnectorType(a.Type); err != nil {
		return err
	}

	if a.Url == "" {
		return fmt.Errorf("url must be specified for connector '%s'", a.Type)
	}
	parsedUrl, err := url.Parse(a.Url)
	if err != nil {
		return fmt.Errorf("invalid url for connector '%s' - %s", a.Type, err.Error())
	}
	if parsedUrl.Scheme == "" || parsedUrl.Host == "" {
		return fmt.Errorf("invalid url for connector '%s' - scheme and host are required", a.Type)
	}
	if strings.HasSuffix(parsedUrl.Hostname(), ".onion") {
		if torProxyUrl == "" {
			return errors.New("tor proxy url is required for onion endpoints")
		}
	}

	return nil
}
