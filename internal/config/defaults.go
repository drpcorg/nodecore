package config

import (
	"github.com/samber/lo"
	"time"
)

func (a *AppConfig) setDefaults() {
	a.UpstreamConfig.setDefaults()
}

func (u *UpstreamConfig) setDefaults() {
	for _, upstream := range u.Upstreams {
		chainDefaults := u.ChainDefaults[upstream.ChainName]
		upstream.setDefaults(chainDefaults)
	}
}

func (u *Upstream) setDefaults(defaults *ChainDefaults) {
	if u.HeadConnector == "" && len(u.Connectors) > 0 {
		filteredConnectors := lo.Filter(u.Connectors, func(item *ConnectorConfig, index int) bool {
			_, ok := connectorTypesRating[item.Type]
			return ok
		})

		if len(filteredConnectors) > 0 {
			defaultHeadConnectorType := lo.MinBy(filteredConnectors, func(a *ConnectorConfig, b *ConnectorConfig) bool {
				return connectorTypesRating[a.Type] < connectorTypesRating[b.Type]
			}).Type
			u.HeadConnector = defaultHeadConnectorType
		}
	}
	if u.PollInterval == 0 {
		u.PollInterval = 1 * time.Minute
	}
	if defaults != nil {
		if defaults.PollInterval != 0 {
			u.PollInterval = defaults.PollInterval
		}
	}
}
