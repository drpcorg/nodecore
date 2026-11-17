package main

import (
	"fmt"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertServerFull(t *testing.T) {
	r := new(report)
	root := mustSection(t, `
version: v2
host: 0.0.0.0
port: 12550
tls:
  enabled: true
  server: {certificate: /c.crt, key: /k.key}
auth:
  enabled: true
  publicKeyOwner: drpc
  server:
    keys: {provider-private-key: /priv.pem, external-public-key: /pub.pem}
monitoring:
  enabled: true
  prometheus: {enabled: true, bind: 0.0.0.0, port: 18881, path: /metrics}
health:
  host: 0.0.0.0
  port: 18082
chain-settings:
  protocols: []
`)
	assert.Equal(t, &config.ServerConfig{
		GrpcPort: 12550, MetricsPort: 18881, HealthPort: 18082,
		TlsConfig:      &config.TlsConfig{Enabled: true, Certificate: "/c.crt", Key: "/k.key"},
		GrpcAuthConfig: &config.GrpcAuthConfig{Enabled: true, PublicKeyOwner: "drpc", ProviderPrivateKeyPath: "/priv.pem", ExternalPublicKeyPath: "/pub.pem"},
	}, convertServer(root, r))
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n\nneeds manual action:\n"+authKeysLine, reportText(t, r))
	assert.Empty(t, root.unread())
}

const authKeysLine = "  auth.server.keys: nodecore does not start unless both key files exist; make them available at these paths where nodecore runs\n"

// The converter cannot check the key files: they rarely exist where the conversion runs. An
// enabled grpc-auth section is therefore always reported, and a disabled one never is.
func TestConvertServerEnabledAuthAsksForTheKeyFiles(t *testing.T) {
	build := func(enabled string) string {
		return "host: 0.0.0.0\nmonitoring: {enabled: false}\nauth:\n  enabled: " + enabled +
			"\n  publicKeyOwner: drpc\n  server:\n    keys: {provider-private-key: /priv.pem, external-public-key: /pub.pem}\n"
	}
	enabled := new(report)
	require.True(t, convertServer(mustSection(t, build("true")), enabled).GrpcAuthConfig.Enabled)
	assert.Contains(t, reportText(t, enabled), authKeysLine)

	disabled := new(report)
	require.False(t, convertServer(mustSection(t, build("false")), disabled).GrpcAuthConfig.Enabled)
	assert.Equal(t, "converted 0 of 0 upstreams -> out.yml\n", reportText(t, disabled))
}

func TestConvertServerDefaults(t *testing.T) {
	r := new(report)
	out := convertServer(mustSection(t, "cluster: {}\n"), r)
	assert.Equal(t, &config.ServerConfig{GrpcPort: 2449, MetricsPort: 8081}, out)
	text := reportText(t, r)
	assert.Contains(t, text, "  host: dshackle listens on 127.0.0.1; nodecore listens on all interfaces, so port 2449 becomes reachable from outside\n")
	assert.Contains(t, text, "  monitoring.prometheus.bind: dshackle listens on 127.0.0.1; nodecore listens on all interfaces, so port 8081 becomes reachable from outside\n")
}

func TestConvertServerRules(t *testing.T) {
	bindLine := func(field, address string, port int) string {
		return fmt.Sprintf("  %s: dshackle listens on %s; nodecore listens on all interfaces, so port %d becomes reachable from outside\n", field, address, port)
	}
	keys := "server: {keys: {provider-private-key: /a, external-public-key: /b}}"
	cases := []struct {
		name, input string
		check       func(t *testing.T, out *config.ServerConfig, text string)
	}{
		{"tls disabled is carried over as disabled", "tls: {enabled: false, server: {certificate: /c, key: /k}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.TlsConfig{Enabled: false, Certificate: "/c", Key: "/k"}, out.TlsConfig)
			assert.NotContains(t, text, "tls")
		}},
		{"tls disabled and incomplete", "tls: {enabled: false, server: {certificate: /c}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.TlsConfig{Enabled: false, Certificate: "/c"}, out.TlsConfig)
			assert.NotContains(t, text, "tls")
		}},
		{"tls disabled still reports client certificates", "tls: {enabled: false, client: {require: true}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.TlsConfig{Enabled: false}, out.TlsConfig)
			assert.Contains(t, text, "  tls.client: nodecore does not verify client certificates; the setting was not carried over\n")
		}},
		{"tls with client certificates", "tls: {server: {certificate: /c, key: /k}, client: {require: true, ca: /ca}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.TlsConfig{Enabled: true, Certificate: "/c", Key: "/k"}, out.TlsConfig)
			assert.Contains(t, text, "  tls.client: nodecore does not verify client certificates; the setting was not carried over\n")
		}},
		{"auth without keys", "auth: {enabled: true, publicKeyOwner: drpc}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Nil(t, out.GrpcAuthConfig)
			assert.Contains(t, text, "  auth: auth is enabled but auth.server.keys is incomplete; grpc-auth was not written (nodecore needs both key paths)\n")
		}},
		{"auth disabled is carried over as disabled", "auth: {enabled: false, publicKeyOwner: drpc, " + keys + "}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.GrpcAuthConfig{Enabled: false, PublicKeyOwner: "drpc", ProviderPrivateKeyPath: "/a", ExternalPublicKeyPath: "/b"}, out.GrpcAuthConfig)
			assert.NotContains(t, text, "auth")
		}},
		{"auth without the enabled key is disabled", "auth: {" + keys + "}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.GrpcAuthConfig{Enabled: false, ProviderPrivateKeyPath: "/a", ExternalPublicKeyPath: "/b"}, out.GrpcAuthConfig)
			assert.NotContains(t, text, "auth")
		}},
		{"auth disabled and incomplete", "auth: {enabled: false, publicKeyOwner: drpc}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.GrpcAuthConfig{Enabled: false, PublicKeyOwner: "drpc"}, out.GrpcAuthConfig)
			assert.NotContains(t, text, "auth")
		}},
		{"auth with a client key", "auth: {enabled: true, client: {private-key: /x}, " + keys + "}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, &config.GrpcAuthConfig{Enabled: true, ProviderPrivateKeyPath: "/a", ExternalPublicKeyPath: "/b"}, out.GrpcAuthConfig)
			assert.Contains(t, text, "  auth.client: only used for dshackle-to-dshackle upstreams\n")
		}},
		{"monitoring disabled", "monitoring: {enabled: false}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Zero(t, out.MetricsPort)
			assert.NotContains(t, text, "monitoring")
		}},
		{"prometheus disabled", "monitoring: {prometheus: {enabled: false}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Zero(t, out.MetricsPort)
			assert.NotContains(t, text, "monitoring")
		}},
		{"prometheus on all interfaces through host", "monitoring: {prometheus: {host: \"::\", port: 9}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, 9, out.MetricsPort)
			assert.NotContains(t, text, "monitoring")
		}},
		{"prometheus on one interface", "monitoring: {prometheus: {bind: 10.0.0.5}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, 8081, out.MetricsPort)
			assert.Contains(t, text, bindLine("monitoring.prometheus.bind", "10.0.0.5", 8081))
		}},
		{"monitoring extras", "monitoring: {jvm: true, extended: true, netty-metrics: {enabled: true}, prometheus: {bind: 0.0.0.0, path: /test}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Contains(t, text, "  monitoring.jvm: no nodecore equivalent\n")
			assert.Contains(t, text, "  monitoring.extended: no nodecore equivalent\n")
			assert.Contains(t, text, "  monitoring.netty-metrics: no nodecore equivalent\n")
			assert.Contains(t, text, "  monitoring.prometheus.path: the metrics path is fixed to /metrics\n")
		}},
		{"health requirements", "health: {blockchains: [{chain: ethereum, min-available: 2}], path: /h}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, 8082, out.HealthPort)
			assert.Contains(t, text, "  health.blockchains: per-chain availability requirements are not supported; use /ready\n")
			assert.Contains(t, text, "  health.path: health endpoints are fixed: /health, /ready, /status\n")
			assert.Contains(t, text, bindLine("health.host", "127.0.0.1", 8082))
		}},
		{"no health section", "port: 1\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Zero(t, out.HealthPort)
			assert.NotContains(t, text, "health")
		}},
		{"warning-only sections", "proxy: {port: 8080}\ncache: {redis: {enabled: true}}\ntokens: []\naccessLog: {enabled: true}\ncompression: {grpc: {}}\npassthrough: true\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Contains(t, text, "  proxy: nodecore has no route-id HTTP proxy; its HTTP endpoint is /queries/<chain> on server.port\n")
			assert.Contains(t, text, "  cache: cache settings are not converted; see nodecore's cache section\n")
			for _, key := range []string{"tokens", "accessLog", "compression", "passthrough"} {
				assert.Contains(t, text, "  "+key+": no nodecore equivalent\n")
			}
		}},
		{"include list", "cluster: {include: [a.yaml, b.yaml]}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Contains(t, text, "  cluster.include: only this file is converted; not read: a.yaml, b.yaml\n")
		}},
		{"include scalar", "cluster: {include: a.yaml}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Contains(t, text, "  cluster.include: only this file is converted; not read: a.yaml\n")
		}},
		{"placeholder in a path", "tls: {server: {certificate: \"${CERT}\", key: /k}}\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, "${CERT}", out.TlsConfig.Certificate)
			assert.Contains(t, text, "  tls.server.certificate: contains a ${VAR} placeholder; nodecore does not substitute environment variables, replace it with the value\n")
		}},
		{"explicit local host", "host: 127.0.0.1\nport: 5000\n", func(t *testing.T, out *config.ServerConfig, text string) {
			assert.Equal(t, 5000, out.GrpcPort)
			assert.Contains(t, text, bindLine("host", "127.0.0.1", 5000))
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			r := new(report)
			root := mustSection(t, test.input)
			out := convertServer(root, r)
			require.NotNil(t, out)
			test.check(t, out, reportText(t, r))
			assert.Empty(t, root.unread())
		})
	}
}

func TestConvertServerEmptyPathsAreMissing(t *testing.T) {
	r := new(report)
	root := mustSection(t, `
tls:
  server: {certificate: "", key: /k}
auth:
  enabled: true
  server:
    keys: {provider-private-key: "", external-public-key: /b}
`)
	out := convertServer(root, r)
	assert.Nil(t, out.TlsConfig)
	assert.Nil(t, out.GrpcAuthConfig)
	text := reportText(t, r)
	assert.Contains(t, text, "  tls: tls.server needs both certificate and key; tls was not written\n")
	assert.Contains(t, text, "  auth: auth is enabled but auth.server.keys is incomplete; grpc-auth was not written (nodecore needs both key paths)\n")
	assert.Empty(t, root.unread())
}
