package server_ctx_test

import (
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/internal/server/server_ctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// plain HTTP headers arrive with canonical casing; the reserved credential
// set must strip them case-insensitively
func TestSanitizeForwardedHeadersStripsCredentialsFromHttpHeaders(t *testing.T) {
	src := http.Header{
		"X-Nodecore-Key":   {"key"},
		"X-Nodecore-Token": {"token"},
		"Authorization":    {"Bearer jwt"},
		"X-Custom":         {"keep"},
	}

	out := server_ctx.SanitizeForwardedHeaders(src)

	assert.Equal(t, http.Header{"X-Custom": {"keep"}}, out)
}

// grpc metadata arrives lowercase; ingress-specific reserved keys ride the
// extras parameter
func TestSanitizeForwardedHeadersStripsCredentialsFromMetadata(t *testing.T) {
	src := metadata.Pairs(
		"x-nodecore-key", "key",
		"x-nodecore-token", "token",
		"authorization", "Bearer jwt",
		"x-nodecore-chain", "sui",
		"x-custom", "keep",
	)

	out := server_ctx.SanitizeForwardedHeaders(src, "X-Nodecore-Chain")

	assert.Equal(t, metadata.Pairs("x-custom", "keep"), out)
}

func TestSanitizeForwardedHeadersDeepCopies(t *testing.T) {
	src := map[string][]string{"x-custom": {"original"}}

	out := server_ctx.SanitizeForwardedHeaders(src)
	require.Equal(t, []string{"original"}, out["x-custom"])
	out["x-custom"][0] = "mutated"

	assert.Equal(t, "original", src["x-custom"][0])
}

func TestSanitizeForwardedHeadersEmptyAndAllReserved(t *testing.T) {
	assert.Nil(t, server_ctx.SanitizeForwardedHeaders(map[string][]string{}))
	assert.Nil(t, server_ctx.SanitizeForwardedHeaders(map[string][]string{"authorization": {"x"}}))
}
