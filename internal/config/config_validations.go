package config

import (
	"errors"
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/drpcorg/dshaltie/pkg/chains"
)

func (a *AppConfig) validate() error {
	if err := a.ServerConfig.validate(); err != nil {
		return err
	}
	if err := a.UpstreamConfig.validate(); err != nil {
		return err
	}
	return nil
}

func (u *UpstreamConfig) validate() error {
	for chain, chainDefault := range u.ChainDefaults {
		if !chains.IsSupported(chain) {
			return fmt.Errorf("error during chain defaults validation, cause: not supported chain %s", chain)
		}
		if err := chainDefault.validate(); err != nil {
			return fmt.Errorf("error during chain %s defaults validation, cause: %s", chain, err.Error())
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
			return fmt.Errorf("error during upstream validation, cause: upstream with id %s already exists", upstream.Id)
		}
		if err := upstream.validate(); err != nil {
			return fmt.Errorf("error during upstream %s validation, cause: %s", upstream.Id, err.Error())
		}
		idSet.Add(upstream.Id)
	}

	return nil
}

func (f *FailsafeConfig) validate() error {
	if err := f.HedgeConfig.validate(); err != nil {
		return fmt.Errorf("hedge config validation error - %s", err.Error())
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

func (s *ServerConfig) validate() error {
	return nil
}

func (u *Upstream) validate() error {
	if !chains.IsSupported(u.ChainName) {
		return fmt.Errorf("not supported chain %s", u.ChainName)
	}

	if len(u.Connectors) == 0 {
		return fmt.Errorf("there must be at least one upstream connector")
	}

	connectorTypeSet := mapset.NewThreadUnsafeSet[ConnectorType]()
	for _, connector := range u.Connectors {
		if connectorTypeSet.Contains(connector.Type) {
			return fmt.Errorf("there can be only one connector of type %s", connector.Type)
		}
		if err := connector.validate(); err != nil {
			return err
		}
		connectorTypeSet.Add(connector.Type)
	}

	if err := u.HeadConnector.validate(); err != nil {
		return fmt.Errorf("invalid head connector - %s", u.HeadConnector)
	}

	if !connectorTypeSet.Contains(u.HeadConnector) {
		return fmt.Errorf("there is no %s connector for head", u.HeadConnector)
	}

	return nil
}

func (c *ChainDefaults) validate() error {
	return nil
}

func (c *ConnectorConfig) validate() error {
	if err := c.Type.validate(); err != nil {
		return err
	}

	if c.Url == "" {
		return fmt.Errorf("url must be specified for connector %s", c.Type)
	}

	return nil
}

func (t ConnectorType) validate() error {
	switch t {
	case Grpc, JsonRpc, Rest, Ws:
	default:
		return fmt.Errorf("invalid connector type - %s", t)
	}
	return nil
}
