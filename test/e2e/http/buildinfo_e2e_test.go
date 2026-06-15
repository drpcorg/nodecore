//go:build e2e

package http_e2e

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/test/e2e/internal/harness"
)

func TestGRPCChainStatusBuildInfoUsesDockerBuildArgsBlackbox(t *testing.T) {
	ctx := context.Background()
	const (
		image   = "nodecore-e2e-buildinfo:latest"
		version = "dev"
		gitSHA  = "e2e-fixed-git-sha"
		want    = "nodecore/dev-e2e-fixed-git-sha"
	)
	harness.BuildNodecoreImageWithBuildInfo(t, image, version, gitSHA)

	networkName, cleanupNetwork := harness.NewNetwork(t, ctx)
	defer cleanupNetwork()

	upstream := harness.StartHardhatNode(t, ctx, networkName, "hardhat-buildinfo", 0)
	defer upstream.Terminate(ctx)

	nodecore := harness.StartNodecoreWithImageAndFiles(t, ctx, networkName, image, nodecoreConfig(dispatchOptions{}, upstream), nil)
	defer nodecore.Terminate(ctx)

	got := chainStatusBuildVersion(t, ctx, nodecore)
	if got != want {
		t.Fatalf("build info version mismatch: got %q want %q\nlogs:\n%s", got, want, nodecore.Logs(ctx))
	}
}

func chainStatusBuildVersion(t *testing.T, ctx context.Context, nodecore *harness.Nodecore) string {
	t.Helper()
	conn, client := harness.GRPCClient(t, nodecore)
	defer conn.Close()
	return harness.WaitForChainStatusBuildVersion(t, ctx, client, nodecore, 60*time.Second)
}
