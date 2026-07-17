package eth_validations

import (
	"context"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	chainspublic "github.com/drpcorg/nodecore/pkg/chains/public"
	"github.com/failsafe-go/failsafe-go"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type compatibleClientsConfig struct {
	Rules []compatibleClientRule `yaml:"rules"`
}

type compatibleClientRule struct {
	Client    string   `yaml:"client"`
	Networks  []string `yaml:"networks"`
	Blacklist []string `yaml:"blacklist"`
}

type EthClientVersionValidator struct {
	upstreamId      string
	connector       connectors.ApiConnector
	chain           *chains.ConfiguredChain
	internalTimeout time.Duration
	rules           []compatibleClientRule
	executor        failsafe.Executor[protocol.ResponseHolder]
}

func NewEthClientVersionValidator(
	upstreamId string,
	connector connectors.ApiConnector,
	chain *chains.ConfiguredChain,
	options *chains.Options,
) *EthClientVersionValidator {
	timeout := time.Second
	if options != nil && options.InternalTimeout > 0 {
		timeout = options.InternalTimeout
	}
	return &EthClientVersionValidator{
		upstreamId:      upstreamId,
		connector:       connector,
		chain:           chain,
		internalTimeout: timeout,
		rules:           loadCompatibleClientRules(),
		executor:        validations.ValidatorExecutor(upstreamId, "ethClientVersionValidation", nil),
	}
}

func (v *EthClientVersionValidator) Validate() validations.ValidationSettingResult {
	if len(v.rules) == 0 || v.chain == nil {
		return validations.Valid
	}

	ctx, cancel := context.WithTimeout(context.Background(), v.internalTimeout)
	defer cancel()

	request, err := protocol.NewInternalUpstreamJsonRpcRequest("web3_clientVersion", nil, v.chain.Chain)
	if err != nil {
		log.Error().Err(err).Msgf("failed to build web3_clientVersion validation request for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	response, err := v.executor.Get(func() (protocol.ResponseHolder, error) {
		return v.connector.SendRequest(ctx, request), nil
	})
	if err != nil {
		log.Warn().Err(err).Msgf("failed to execute client version validation request for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}
	if response.HasError() {
		log.Warn().Err(response.GetError()).Msgf("failed to read client version for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	rawVersion, err := response.ResponseResultString()
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse client version response for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}
	rawVersion = strings.Trim(rawVersion, "\"")

	detector := eth_labels.NewEthClientLabelsDetector(v.upstreamId, v.chain.Chain, eth_labels.EthMappingFunc, func() (protocol.RequestHolder, error) {
		return protocol.NewInternalUpstreamJsonRpcRequest("web3_clientVersion", nil, v.chain.Chain)
	})
	clientVersion, clientType, err := detector.ClientVersionAndType(response.ResponseResult())
	if err != nil {
		log.Warn().Err(err).Msgf("failed to detect client type/version for upstream '%s'", v.upstreamId)
		return validations.SettingsError
	}

	for _, rule := range v.rules {
		if !v.ruleApplies(rule, clientType) {
			continue
		}
		for _, denied := range rule.Blacklist {
			if clientVersionDenied(rawVersion, clientVersion, denied) {
				log.Warn().Msgf("upstream '%s' uses incompatible client '%s' version '%s' on chain '%s'; rule denies '%s'", v.upstreamId, clientType, clientVersion, v.chain.Chain.String(), denied)
				return validations.FatalSettingError
			}
		}
	}

	return validations.Valid
}

func (v *EthClientVersionValidator) ruleApplies(rule compatibleClientRule, clientType string) bool {
	if !strings.EqualFold(rule.Client, clientType) {
		return false
	}
	if len(rule.Networks) == 0 {
		return true
	}
	chainNames := map[string]struct{}{strings.ToLower(v.chain.Chain.String()): {}}
	for _, shortName := range v.chain.ShortNames {
		chainNames[strings.ToLower(shortName)] = struct{}{}
	}
	for _, network := range rule.Networks {
		if _, ok := chainNames[strings.ToLower(network)]; ok {
			return true
		}
	}
	return false
}

func loadCompatibleClientRules() []compatibleClientRule {
	var cfg compatibleClientsConfig
	if err := yaml.Unmarshal(chainspublic.GetCompatibleClients(), &cfg); err != nil {
		log.Warn().Err(err).Msg("failed to load compatible clients config")
		return nil
	}
	return cfg.Rules
}

func clientVersionDenied(rawVersion, clientVersion, denied string) bool {
	rawVersion = normalizeClientVersion(rawVersion)
	clientVersion = normalizeClientVersion(clientVersion)
	denied = normalizeClientVersion(denied)
	return rawVersion == denied || clientVersion == denied
}

func normalizeClientVersion(version string) string {
	version = strings.TrimSpace(strings.Trim(version, "\""))
	version = strings.TrimPrefix(version, "v")
	return strings.ToLower(version)
}
