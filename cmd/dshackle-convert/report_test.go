package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReportRendering(t *testing.T) {
	r := new(report)
	r.skip("xrp-1", "chain 'ripple' is not served by nodecore")
	r.drop("eth-1", "node-id", "not used by nodecore")
	r.manual("", "tls.client", "nodecore does not verify client certificates; the setting was not carried over")
	r.drop("", "proxy", "nodecore has no route-id HTTP proxy; its HTTP endpoint is /queries/<chain> on server.port")
	r.drop("eth-2", "node-id", "not used by nodecore")

	var out strings.Builder
	r.write(&out, 2, 3, "nodecore.yml")
	assert.Equal(t, `converted 2 of 3 upstreams -> nodecore.yml

skipped upstreams:
  xrp-1: chain 'ripple' is not served by nodecore

needs manual action:
  tls.client: nodecore does not verify client certificates; the setting was not carried over

not carried over:
  node-id (eth-1, eth-2): not used by nodecore
  proxy: nodecore has no route-id HTTP proxy; its HTTP endpoint is /queries/<chain> on server.port
`, out.String())
}

func TestReportEmpty(t *testing.T) {
	var out strings.Builder
	new(report).write(&out, 1, 1, "o.yml")
	assert.Equal(t, "converted 1 of 1 upstreams -> o.yml\n", out.String())
}
