package main

import (
	"fmt"
	"strings"

	"github.com/drpcorg/nodecore/internal/config"
)

const (
	defaultGrpcPort    = 2449
	defaultMetricsPort = 8081
	defaultHealthPort  = 8082
	defaultBindAddress = "127.0.0.1"
	defaultMetricsPath = "/metrics"
)

// convertServer converts everything outside `cluster` and reports the top-level sections that
// have no place in a nodecore config. It also reports cluster.include. It never returns nil:
// the gRPC port is always written, because nodecore's gRPC server is off unless it is set.
func convertServer(root *section, r *report) *config.ServerConfig {
	server := &config.ServerConfig{GrpcPort: defaultGrpcPort}
	if port, ok := root.integer("port"); ok {
		server.GrpcPort = int(port)
	}
	reportBind(root, []string{"host"}, "host", server.GrpcPort, r)

	server.TlsConfig = convertTls(root, r)
	server.GrpcAuthConfig = convertAuth(root, r)
	server.MetricsPort = convertMonitoring(root, r)
	server.HealthPort = convertHealth(root, r)

	dropField(root, "proxy", "", "proxy", "nodecore has no route-id HTTP proxy; its HTTP endpoint is /queries/<chain> on server.port", r)
	dropField(root, "cache", "", "cache", "cache settings are not converted; see nodecore's cache section", r)
	for _, key := range []string{"tokens", "accessLog", "compression", "passthrough"} {
		dropField(root, key, "", key, noEquivalentMessage, r)
	}
	// agreed to be left out without a word
	root.ignore("version")
	root.ignore("chain-settings")

	if included := root.sub("cluster").strings("include"); len(included) > 0 {
		r.drop("", "cluster.include", "only this file is converted; not read: "+strings.Join(included, ", "))
	}
	return server
}

// convertTls maps dshackle's server TLS. A section that is switched off is written switched
// off, with whatever paths it holds, so the operator sees it and only has to flip the flag;
// nodecore checks the paths only when TLS is on. A section that is on but lacks a path is
// reported instead of written, because nodecore would refuse it.
func convertTls(root *section, r *report) *config.TlsConfig {
	tls := root.sub("tls")
	if !tls.exists() {
		return nil
	}
	if tls.has("client") {
		tls.ignore("client")
		r.manual("", "tls.client", "nodecore does not verify client certificates; the setting was not carried over")
	}
	// dshackle turns TLS on when the section is there, unless it says `enabled: false`
	enabled := true
	if configured, ok := tls.boolean("enabled"); ok {
		enabled = configured
	}
	keys := tls.sub("server")
	certificate, _ := keys.str("certificate")
	key, _ := keys.str("key")
	if enabled && (certificate == "" || key == "") {
		r.manual("", "tls", "tls.server needs both certificate and key; tls was not written")
		return nil
	}
	reportPlaceholder(certificate, "", "tls.server.certificate", r)
	reportPlaceholder(key, "", "tls.server.key", r)
	return &config.TlsConfig{Enabled: enabled, Certificate: certificate, Key: key}
}

// convertAuth maps dshackle's auth section to grpc-auth, by the same rule as TLS: a section
// that is off is written off with what it holds, and one that is on without both key paths is
// reported instead of written, because nodecore rejects it.
func convertAuth(root *section, r *report) *config.GrpcAuthConfig {
	auth := root.sub("auth")
	if !auth.exists() {
		return nil
	}
	dropField(auth, "client", "", "auth.client", "only used for dshackle-to-dshackle upstreams", r)
	enabled, _ := auth.boolean("enabled")
	keys := auth.sub("server").sub("keys")
	privateKey, _ := keys.str("provider-private-key")
	publicKey, _ := keys.str("external-public-key")
	owner, _ := auth.str("publicKeyOwner")
	if enabled && (privateKey == "" || publicKey == "") {
		r.manual("", "auth", "auth is enabled but auth.server.keys is incomplete; grpc-auth was not written (nodecore needs both key paths)")
		return nil
	}
	reportPlaceholder(privateKey, "", "auth.server.keys.provider-private-key", r)
	reportPlaceholder(publicKey, "", "auth.server.keys.external-public-key", r)
	reportPlaceholder(owner, "", "auth.publicKeyOwner", r)
	if enabled {
		// the one check of nodecore's validation the converter cannot run: the key files
		// rarely exist where the conversion runs
		r.manual("", "auth.server.keys", "nodecore does not start unless both key files exist; make them available at these paths where nodecore runs")
	}
	return &config.GrpcAuthConfig{
		Enabled:                enabled,
		PublicKeyOwner:         owner,
		ProviderPrivateKeyPath: privateKey,
		ExternalPublicKeyPath:  publicKey,
	}
}

// convertMonitoring returns the metrics port, or 0 when dshackle's metrics are switched off.
// dshackle serves metrics by default, also without a `monitoring` section.
func convertMonitoring(root *section, r *report) int {
	monitoring := root.sub("monitoring")
	prometheus := monitoring.sub("prometheus")
	if enabled, ok := monitoring.boolean("enabled"); ok && !enabled {
		root.ignore("monitoring")
		return 0
	}
	if enabled, ok := prometheus.boolean("enabled"); ok && !enabled {
		root.ignore("monitoring")
		return 0
	}
	for _, key := range []string{"jvm", "JVM", "extended", "netty-metrics"} {
		dropField(monitoring, key, "", "monitoring."+key, noEquivalentMessage, r)
	}
	if path, ok := prometheus.str("path"); ok && path != defaultMetricsPath {
		r.drop("", "monitoring.prometheus.path", "the metrics path is fixed to /metrics")
	}
	metricsPort := defaultMetricsPort
	if port, ok := prometheus.integer("port"); ok {
		metricsPort = int(port)
	}
	reportBind(prometheus, []string{"bind", "host"}, "monitoring.prometheus.bind", metricsPort, r)
	return metricsPort
}

// convertHealth returns the health port, or 0 when the input has no `health` section and
// nodecore's own default applies.
func convertHealth(root *section, r *report) int {
	health := root.sub("health")
	if !health.exists() {
		return 0
	}
	dropField(health, "path", "", "health.path", "health endpoints are fixed: /health, /ready, /status", r)
	dropField(health, "blockchains", "", "health.blockchains", "per-chain availability requirements are not supported; use /ready", r)
	healthPort := defaultHealthPort
	if port, ok := health.integer("port"); ok {
		healthPort = int(port)
	}
	reportBind(health, []string{"host"}, "health.host", healthPort, r)
	return healthPort
}

// reportBind warns when dshackle bound a listener to one interface: nodecore listens on all
// of them, so the port becomes reachable from outside. keys are tried in order; a listener
// with none of them set uses dshackle's default, the loopback address.
func reportBind(owner *section, keys []string, field string, port int, r *report) {
	address := defaultBindAddress
	for _, key := range keys {
		if configured, ok := owner.str(key); ok {
			address = configured
			break
		}
	}
	for _, key := range keys {
		owner.ignore(key)
	}
	if address == "0.0.0.0" || address == "::" {
		return
	}
	r.manual("", field, fmt.Sprintf(
		"dshackle listens on %s; nodecore listens on all interfaces, so port %d becomes reachable from outside", address, port))
}
