package config

import (
	"fmt"
	mapset "github.com/deckarep/golang-set/v2"
)

func (a *AppConfig) Validate() error {
	if err := a.ServerConfig.Validate(); err != nil {
		return err
	}
	for _, project := range a.Projects {
		if err := project.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (p *ProjectConfig) Validate() error {
	idSet := mapset.NewThreadUnsafeSet[string]()
	for _, upstream := range p.Upstreams {
		if idSet.Contains(upstream.Id) {
			return fmt.Errorf("id %s already exists", upstream.Id)
		}

		if err := upstream.Validate(); err != nil {
			return fmt.Errorf("error during validation upstream %s, cuase - %s", upstream.Id, err.Error())
		}
		idSet.Add(upstream.Id)
	}
	return nil
}

func (t *TlsConfig) Validate() error {
	return nil
}

func (s *ServerConfig) Validate() error {
	return nil
}

func (u *UpstreamConfig) Validate() error {
	if len(u.Connectors) == 0 {
		return fmt.Errorf("there must be at least one upstream connector")
	}
	for _, connector := range u.Connectors {
		if err := connector.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *ConnectorConfig) Validate() error {
	switch c.Type {
	case Grpc, JsonRpc, Rest, Ws:
	default:
		return fmt.Errorf("invalid connector type - %s", c.Type)
	}
	return nil
}
