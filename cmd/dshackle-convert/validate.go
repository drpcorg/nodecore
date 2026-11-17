package main

import (
	"fmt"

	"github.com/drpcorg/nodecore/internal/config"
)

const (
	dummyWebsocketUrl = "ws://placeholder.invalid"
	dummyHttpUrl      = "http://placeholder.invalid"
)

// validateUpstream checks one converted upstream with nodecore's own parser, defaults and
// validators, in a document that holds the shared settings and only this upstream. An
// upstream nodecore would refuse (typically a connector type the chain's method spec does not
// declare) is found here, so it can be left out without failing the whole conversion.
func validateUpstream(cfg *config.AppConfig, up *config.Upstream) error {
	return validateDocument(&config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              cfg.UpstreamConfig.Mode,
			BalancingStrategy: cfg.UpstreamConfig.BalancingStrategy,
			ChainDefaults:     cfg.UpstreamConfig.ChainDefaults,
			Upstreams:         []*config.Upstream{up},
		},
	})
}

// validateChainDefaults checks the defaults of one chain, next to a stand-in upstream of that
// chain. Without it a value nodecore refuses in cluster.defaults would fail the validation of
// every upstream, whatever its chain.
func validateChainDefaults(cfg *config.AppConfig, chainName string, defaults *config.ChainDefaults) error {
	_, chain, ok := resolveChain(chainName)
	if !ok {
		return fmt.Errorf("chain '%s' is not served by nodecore", chainName)
	}
	return validateDocument(&config.AppConfig{
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              cfg.UpstreamConfig.Mode,
			BalancingStrategy: cfg.UpstreamConfig.BalancingStrategy,
			ChainDefaults:     map[string]*config.ChainDefaults{chainName: defaults},
			Upstreams: []*config.Upstream{{
				Id:         "chain-defaults-check",
				ChainName:  chainName,
				Connectors: []*config.ApiConnectorConfig{{Type: primaryConnectorType(chain.Type), Url: dummyHttpUrl}},
			}},
		},
	})
}

// validateConfig checks the whole output, which catches what a single upstream cannot show:
// duplicate ids and clashing ports. grpc-auth is left out of the checked copy, because its
// validator requires the key files to exist on disk and they rarely exist where the
// conversion runs.
func validateConfig(cfg *config.AppConfig) error {
	checked := &config.AppConfig{UpstreamConfig: cfg.UpstreamConfig}
	if cfg.ServerConfig != nil {
		checked.ServerConfig = &config.ServerConfig{
			GrpcPort:    cfg.ServerConfig.GrpcPort,
			MetricsPort: cfg.ServerConfig.MetricsPort,
			HealthPort:  cfg.ServerConfig.HealthPort,
			TlsConfig:   cfg.ServerConfig.TlsConfig,
		}
	}
	return validateDocument(checked)
}

// validateDocument runs nodecore's validation on the marshalled document. LoadReloadable with
// the same bytes twice is exactly parse + defaults + validate, and it is the entry point of
// the config package that takes bytes.
//
// A connector URL that still holds a ${VAR} placeholder may not parse as a URL at all. The
// upstream is kept regardless, so in the checked copy such a URL is swapped for a valid one
// and the rest of the upstream is still validated.
func validateDocument(doc *config.AppConfig) error {
	checked := &config.AppConfig{
		ServerConfig: doc.ServerConfig,
		UpstreamConfig: &config.UpstreamConfig{
			Mode:              doc.UpstreamConfig.Mode,
			BalancingStrategy: doc.UpstreamConfig.BalancingStrategy,
			ChainDefaults:     doc.UpstreamConfig.ChainDefaults,
			Upstreams:         withoutPlaceholderUrls(doc.UpstreamConfig.Upstreams),
		},
	}
	data, err := marshalConfig(checked)
	if err != nil {
		return err
	}
	_, err = config.LoadReloadable(data, data)
	return err
}

func withoutPlaceholderUrls(upstreams []*config.Upstream) []*config.Upstream {
	checked := make([]*config.Upstream, 0, len(upstreams))
	for _, upstream := range upstreams {
		copied := *upstream
		copied.Connectors = make([]*config.ApiConnectorConfig, 0, len(upstream.Connectors))
		for _, connector := range upstream.Connectors {
			replaced := *connector
			if hasPlaceholder(connector.Url) {
				replaced.Url = dummyHttpUrl
				if connector.Type == websocketType {
					replaced.Url = dummyWebsocketUrl
				}
			}
			copied.Connectors = append(copied.Connectors, &replaced)
		}
		checked = append(checked, &copied)
	}
	return checked
}
