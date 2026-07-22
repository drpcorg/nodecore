package utils_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resolvedIP(t *testing.T, remoteAddr, xff string, trustedCIDRs []string) string {
	t.Helper()
	trusted, err := utils.ParseTrustedProxies(trustedCIDRs)
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	ctx := utils.ContextWithIps(context.Background(), req, trusted)
	ips := utils.IpsFromContext(ctx).ToSlice()
	require.Len(t, ips, 1, "exactly one client IP must be resolved")
	return ips[0]
}

func TestClientIP_UntrustedPeerIgnoresSpoofedXFF(t *testing.T) {
	// No trusted proxies: a direct client cannot spoof its IP via X-Forwarded-For.
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4:5555", "9.9.9.9", nil))
}

func TestClientIP_NoXFFUsesPeer(t *testing.T) {
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4:5555", "", nil))
}

func TestClientIP_TrustedPeerTakesXFF(t *testing.T) {
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", "9.9.9.9", []string{"10.0.0.0/8"}))
}

func TestClientIP_TrustedPeerTakesRightmostUntrusted(t *testing.T) {
	// The trusted proxy appends the connecting IP on the right; trusted hops are skipped.
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", "9.9.9.9, 10.0.0.2", []string{"10.0.0.0/8"}))
}

func TestClientIP_AttackerPrependedXFFEntryIgnored(t *testing.T) {
	// Attacker sends "X-Forwarded-For: 6.6.6.6"; the trusted proxy appends the real
	// client (9.9.9.9). Rightmost-untrusted wins, so the spoofed 6.6.6.6 is ignored.
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", "6.6.6.6, 9.9.9.9", []string{"10.0.0.0/8"}))
}

func TestClientIP_AllHopsTrustedFallsBackToPeer(t *testing.T) {
	assert.Equal(t, "10.0.0.1", resolvedIP(t, "10.0.0.1:5555", "10.0.0.2, 10.0.0.3", []string{"10.0.0.0/8"}))
}

func TestClientIP_TrustedPeerNoXFFUsesPeer(t *testing.T) {
	assert.Equal(t, "10.0.0.1", resolvedIP(t, "10.0.0.1:5555", "", []string{"10.0.0.0/8"}))
}

func TestClientIP_EmptyRemoteAddrDefaults(t *testing.T) {
	assert.Equal(t, "127.0.0.1", resolvedIP(t, "", "", nil))
}

func TestClientIP_BareIPRemoteAddr(t *testing.T) {
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4", "", nil))
}

func TestParseTrustedProxies(t *testing.T) {
	t.Run("cidr", func(t *testing.T) {
		p, err := utils.ParseTrustedProxies([]string{"10.0.0.0/8"})
		require.NoError(t, err)
		assert.Len(t, p, 1)
	})
	t.Run("bare ip becomes /32", func(t *testing.T) {
		p, err := utils.ParseTrustedProxies([]string{"192.168.1.1"})
		require.NoError(t, err)
		require.Len(t, p, 1)
		assert.Equal(t, 32, p[0].Bits())
	})
	t.Run("blank entries skipped", func(t *testing.T) {
		p, err := utils.ParseTrustedProxies([]string{"", "  "})
		require.NoError(t, err)
		assert.Empty(t, p)
	})
	t.Run("invalid returns error", func(t *testing.T) {
		_, err := utils.ParseTrustedProxies([]string{"not-an-ip"})
		assert.Error(t, err)
	})
}
