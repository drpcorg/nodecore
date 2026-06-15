//go:build e2e

package http_e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPReadinessStaysUnavailableWithoutHealthyUpstreamBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	nodecore := harness.StartNodecore(t, ctx, networkName, unhealthyNodecoreConfig())
	defer nodecore.Terminate(ctx)

	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	assertReadyNeverOK(t, nodecore)
}

func assertReadyNeverOK(t *testing.T, nodecore *harness.Nodecore) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		last = getStatus(t, nodecore.HealthURL+"/ready")
		if last == http.StatusOK {
			t.Fatalf("/ready unexpectedly returned 200 for unhealthy upstream; status=%+v\nlogs:\n%s", getJSON(t, nodecore.HealthURL+"/status"), nodecore.Logs(context.Background()))
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last != http.StatusServiceUnavailable {
		t.Fatalf("/ready status=%d want 503; status body=%+v\nlogs:\n%s", last, getJSON(t, nodecore.HealthURL+"/status"), nodecore.Logs(context.Background()))
	}
}

func unhealthyNodecoreConfig() string {
	return fmt.Sprintf(`server:
  port: 8080
  grpc-port: 9090
  metrics-port: 0
  pprof-port: 0
  health-port: 9091
  grpc-auth:
    enabled: false
upstream-config:
  mode: default
  score-policy-config:
    calculation-interval: 500ms
  upstreams:
    - id: unreachable-hardhat
      chain: ethereum
      poll-interval: 1s
      connectors:
        - type: json-rpc
          url: %q
      options:
        internal-timeout: 500ms
        validation-interval: 1s
        disable-lower-bounds-detection: true
        disable-labels-detection: true
        validate-syncing: false
        validate-peers: false
`, "http://unreachable-hardhat:8545")
}
