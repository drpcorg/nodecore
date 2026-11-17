package main

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg(ups ...*config.Upstream) *config.AppConfig {
	return &config.AppConfig{ServerConfig: &config.ServerConfig{GrpcPort: 2449},
		UpstreamConfig: &config.UpstreamConfig{Mode: "strict", BalancingStrategy: "base", Upstreams: ups}}
}
func jsonRpc(id, chain, url string) *config.Upstream {
	return &config.Upstream{Id: id, ChainName: chain, Connectors: []*config.ApiConnectorConfig{{Type: "json-rpc", Url: url}}}
}

func TestValidateUpstream(t *testing.T) {
	ok := jsonRpc("eth-1", "ethereum", "http://n:1")
	assert.NoError(t, validateUpstream(validCfg(ok), ok))

	ws := &config.Upstream{Id: "near-1", ChainName: "near", Connectors: []*config.ApiConnectorConfig{
		{Type: "json-rpc", Url: "http://n:1"}, {Type: "websocket", Url: "ws://n:2"}}}
	err := validateUpstream(validCfg(ws), ws)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "websocket")
}

func TestValidatePlaceholderUrlsAreSubstituted(t *testing.T) {
	up := &config.Upstream{Id: "a", ChainName: "ethereum", Connectors: []*config.ApiConnectorConfig{
		{Type: "json-rpc", Url: "${NODE_URL}"}, {Type: "websocket", Url: "ws://${HOST}:8546"}}}
	cfg := validCfg(up)
	assert.NoError(t, validateUpstream(cfg, up))
	assert.NoError(t, validateConfig(cfg))
	assert.Equal(t, "${NODE_URL}", up.Connectors[0].Url) // the original is untouched
}

func TestValidateConfig(t *testing.T) {
	// grpc-auth key files do not exist on the conversion host: must not fail
	cfg := validCfg(jsonRpc("eth-1", "ethereum", "http://n:1"))
	cfg.ServerConfig.GrpcAuthConfig = &config.GrpcAuthConfig{Enabled: true, ProviderPrivateKeyPath: "/missing/a", ExternalPublicKeyPath: "/missing/b"}
	assert.NoError(t, validateConfig(cfg))
	assert.NotNil(t, cfg.ServerConfig.GrpcAuthConfig)

	dup := validCfg(jsonRpc("eth-1", "ethereum", "http://n:1"), jsonRpc("eth-1", "ethereum", "http://n:2"))
	err := validateConfig(dup)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream with id 'eth-1' already exists")

	clash := validCfg(jsonRpc("eth-1", "ethereum", "http://n:1"))
	clash.ServerConfig.GrpcPort = 9090 // nodecore's default HTTP port
	assert.Error(t, validateConfig(clash))
}
