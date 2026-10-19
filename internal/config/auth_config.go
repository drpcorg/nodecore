package config

import (
	"errors"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
)

type AuthConfig struct {
	Enabled               bool                   `yaml:"enabled"`
	RequestStrategyConfig *RequestStrategyConfig `yaml:"request-strategy"`
	KeyConfigs            []*KeyConfig           `yaml:"key-management"`
}

type RequestStrategyConfig struct {
	Type                       RequestStrategyType         `yaml:"type"`
	TokenRequestStrategyConfig *TokenRequestStrategyConfig `yaml:"token"`
	JwtRequestStrategyConfig   *JwtRequestStrategyConfig   `yaml:"jwt"`
}

type KeyConfig struct {
	Id             string          `yaml:"id"`
	Type           IntegrationType `yaml:"type"`
	LocalKeyConfig *LocalKeyConfig `yaml:"local"`
	DrpcKeyConfig  *DrpcKeyConfig  `yaml:"drpc"`
}

func (k *KeyConfig) GetSpecificKeyConfig() IntegrationKeyConfig {
	if k.LocalKeyConfig != nil {
		return k.LocalKeyConfig
	} else if k.DrpcKeyConfig != nil {
		return k.DrpcKeyConfig
	}
	return nil
}

type RequestStrategyType string

const (
	Token RequestStrategyType = "token"
	Jwt   RequestStrategyType = "jwt"
)

type TokenRequestStrategyConfig struct {
	Value string `yaml:"value"`
}

type JwtRequestStrategyConfig struct {
	PublicKey          string `yaml:"public-key"`
	AllowedIssuer      string `yaml:"allowed-issuer"`
	ExpirationRequired bool   `yaml:"expiration-required"`
}

type DrpcKeyConfig struct {
	Owner *DrpcOwnerConfig `yaml:"owner"`
}

type DrpcOwnerConfig struct {
	Id       string `yaml:"id"`
	ApiToken string `yaml:"api-token"`
}

type LocalKeyConfig struct {
	Key               string             `yaml:"key"`
	KeySettingsConfig *KeySettingsConfig `yaml:"settings"`
}

type KeySettingsConfig struct {
	AllowedIps    []string       `yaml:"allowed-ips"`
	Methods       *AuthMethods   `yaml:"methods"`
	AuthContracts *AuthContracts `yaml:"contracts"`
	CorsOrigins   []string       `yaml:"cors-origins"`
	Upstreams     *KeyUpstreams  `yaml:"upstreams"`
}

// KeyUpstreams restricts which upstreams may serve a key's requests. It is a
// filter, not a balancer: the chain's balancing strategy (rating, base or
// label-balancing) still picks among the upstreams that pass it.
type KeyUpstreams struct {
	// GroupLabels admits an upstream that carries at least one of these
	// config group-labels (the same labels label-balancing groups by).
	GroupLabels []string `yaml:"group-labels"`
}

type AuthMethods struct {
	Allowed   []string `yaml:"allowed"`
	Forbidden []string `yaml:"forbidden"`
}

type AuthContracts struct {
	Allowed []string `yaml:"allowed"`
}

type IntegrationKeyConfig interface {
	keyCfg()
}

type ExternalKeyConfig struct {
}

func (d *ExternalKeyConfig) keyCfg() {}

func (d *DrpcKeyConfig) keyCfg() {}

func (l *LocalKeyConfig) keyCfg() {}

func (a *AuthConfig) validate(integrationCfg *IntegrationConfig, upstreamCfg *UpstreamConfig) error {
	if !a.Enabled {
		return nil
	}
	if a.RequestStrategyConfig != nil {
		if err := a.RequestStrategyConfig.validate(); err != nil {
			return err
		}
	}
	if len(a.KeyConfigs) > 0 {
		keyIds := mapset.NewThreadUnsafeSet[string]()
		keys := mapset.NewThreadUnsafeSet[string]()
		groupLabels := upstreamGroupLabels(upstreamCfg)
		for i, keyConfig := range a.KeyConfigs {
			if keyConfig.Id == "" {
				return fmt.Errorf("error during key config validation, cause: no key id under index %d", i)
			}
			if keyIds.ContainsOne(keyConfig.Id) {
				return fmt.Errorf("error during key config validation, key with id '%s' already exists", keyConfig.Id)
			}
			if keyConfig.LocalKeyConfig != nil && keys.ContainsOne(keyConfig.LocalKeyConfig.Key) {
				return fmt.Errorf("error during key config validation, local key '%s' already exists", keyConfig.LocalKeyConfig.Key)
			}
			if err := keyConfig.validate(integrationCfg, groupLabels); err != nil {
				return fmt.Errorf("error during '%s' key config validation, cause: %s", keyConfig.Id, err.Error())
			}
			keyIds.Add(keyConfig.Id)
			if keyConfig.LocalKeyConfig != nil {
				keys.Add(keyConfig.LocalKeyConfig.Key)
			}
		}
	}
	return nil
}

func (r *RequestStrategyConfig) validate() error {
	if err := r.Type.validate(); err != nil {
		return err
	}
	switch r.Type {
	case Token:
		if r.TokenRequestStrategyConfig == nil {
			return fmt.Errorf("specified '%s' request strategy type but there are no its settings", r.Type)
		}
		if err := r.TokenRequestStrategyConfig.validate(); err != nil {
			return fmt.Errorf("error during '%s' request strategy validation, cause: %s", r.Type, err.Error())
		}
	case Jwt:
		if r.JwtRequestStrategyConfig == nil {
			return fmt.Errorf("specified '%s' request strategy type but there are no its settings", r.Type)
		}
		if err := r.JwtRequestStrategyConfig.validate(); err != nil {
			return fmt.Errorf("error during '%s' request strategy validation, cause: %s", r.Type, err.Error())
		}
	}

	return nil
}

func (k *KeyConfig) validate(integrationCfg *IntegrationConfig, groupLabels mapset.Set[string]) error {
	if err := k.Type.validate(); err != nil {
		return err
	}
	switch k.Type {
	case Local:
		if k.LocalKeyConfig == nil {
			return keyNoSettingsError(k.Type)
		}
		if err := k.LocalKeyConfig.validate(groupLabels); err != nil {
			return err
		}
	case Drpc:
		if integrationCfg == nil || integrationCfg.Drpc == nil {
			return errors.New("there is no drpc integration for drpc keys")
		}
		if k.DrpcKeyConfig == nil {
			return keyNoSettingsError(k.Type)
		}
		if err := k.DrpcKeyConfig.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (d *DrpcKeyConfig) validate() error {
	if d.Owner == nil {
		return errors.New("owner config is empty")
	}
	if d.Owner.Id == "" {
		return errors.New("owner id is empty")
	}
	if d.Owner.ApiToken == "" {
		return errors.New("owner API token is empty")
	}
	return nil
}

func keyNoSettingsError(keyType IntegrationType) error {
	return fmt.Errorf("specified '%s' key management rule type but there are no its settings", keyType)
}

func (l *LocalKeyConfig) validate(groupLabels mapset.Set[string]) error {
	if l.Key == "" {
		return errors.New("'key' field is empty")
	}
	if l.KeySettingsConfig != nil && l.KeySettingsConfig.Upstreams != nil {
		if err := l.KeySettingsConfig.Upstreams.validate(groupLabels); err != nil {
			return err
		}
	}
	return nil
}

// validate also rejects a label no upstream carries (groupLabels): such a key
// would silently be served by nothing, and a typo is far likelier than an
// intent to disable the key.
func (k *KeyUpstreams) validate(groupLabels mapset.Set[string]) error {
	if len(k.GroupLabels) == 0 {
		return errors.New("upstreams.group-labels must contain at least one label")
	}
	seen := mapset.NewThreadUnsafeSet[string]()
	for _, label := range k.GroupLabels {
		if label == "" {
			return errors.New("upstreams.group-labels must not contain an empty label")
		}
		if seen.Contains(label) {
			return fmt.Errorf("upstreams.group-labels contains a duplicate label '%s'", label)
		}
		if !groupLabels.Contains(label) {
			return fmt.Errorf("upstreams.group-labels has '%s', which no upstream carries", label)
		}
		seen.Add(label)
	}
	return nil
}

// upstreamGroupLabels collects the group-labels carried by any configured
// upstream.
func upstreamGroupLabels(upstreamCfg *UpstreamConfig) mapset.Set[string] {
	groupLabels := mapset.NewThreadUnsafeSet[string]()
	if upstreamCfg != nil {
		for _, upstream := range upstreamCfg.Upstreams {
			groupLabels.Append(upstream.GroupLabels...)
		}
	}
	return groupLabels
}

func (r RequestStrategyType) validate() error {
	switch r {
	case Token, Jwt:
	default:
		return fmt.Errorf("invalid request strategy type - '%s'", r)
	}
	return nil
}

func (j *JwtRequestStrategyConfig) validate() error {
	if j.PublicKey == "" {
		return errors.New("there is no the public key path")
	}
	return nil
}

func (t *TokenRequestStrategyConfig) validate() error {
	if t.Value == "" {
		return errors.New("there is no secret value")
	}
	return nil
}
