//go:build e2e

package http_e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestHTTPHealthAndReadinessBlackbox(t *testing.T) {
	ctx := context.Background()
	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-health", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecore(t, ctx, networkName, nodecoreConfig(dispatchOptions{}, upstream))
	defer nodecore.Terminate(ctx)

	assertStatus(t, nodecore.HealthURL+"/health", http.StatusOK)
	waitReady(t, nodecore)
}

func waitReady(t *testing.T, nodecore *harness.Nodecore) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var lastStatus int
	var lastBody map[string]any
	for time.Now().Before(deadline) {
		lastStatus = getStatus(t, nodecore.HealthURL+"/ready")
		lastBody = getJSON(t, nodecore.HealthURL+"/status")
		if lastStatus == http.StatusOK && lastBody["ready"] == true {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("nodecore did not become ready; /ready=%d /status=%+v\nlogs:\n%s", lastStatus, lastBody, nodecore.Logs(context.Background()))
}

func assertStatus(t *testing.T, url string, want int) {
	t.Helper()
	if got := getStatus(t, url); got != want {
		t.Fatalf("GET %s status=%d want=%d", url, got, want)
	}
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}
