package utils_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRequest(t *testing.T, remoteAddr string, xffValues ...string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	req.RemoteAddr = remoteAddr
	for _, xff := range xffValues {
		req.Header.Add("X-Forwarded-For", xff)
	}
	return req
}

// resolvedIPs returns every IP stored in the context.
func resolvedIPs(t *testing.T, remoteAddr string, trustedCIDRs []string, xffValues ...string) []string {
	t.Helper()
	trusted, err := utils.ParseTrustedProxies(trustedCIDRs)
	require.NoError(t, err)
	ctx := utils.ContextWithIps(context.Background(), newRequest(t, remoteAddr, xffValues...), trusted)
	return utils.IpsFromContext(ctx).ToSlice()
}

// resolvedIP asserts a single client IP was resolved and returns it.
func resolvedIP(t *testing.T, remoteAddr string, trustedCIDRs []string, xffValues ...string) string {
	t.Helper()
	ips := resolvedIPs(t, remoteAddr, trustedCIDRs, xffValues...)
	require.Len(t, ips, 1, "exactly one client IP must be resolved")
	return ips[0]
}

// With trusted proxies configured a single client IP is resolved.

func TestClientIP_UntrustedPeerIgnoresSpoofedXFF(t *testing.T) {
	// The peer is not a trusted proxy, so it cannot spoof its IP via X-Forwarded-For.
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4:5555", []string{"10.0.0.0/8"}, "9.9.9.9"))
}

func TestClientIP_TrustedPeerTakesXFF(t *testing.T) {
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}, "9.9.9.9"))
}

func TestClientIP_TrustedPeerTakesRightmostUntrusted(t *testing.T) {
	// The trusted proxy appends the connecting IP on the right; trusted hops are skipped.
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}, "9.9.9.9, 10.0.0.2"))
}

func TestClientIP_AttackerPrependedXFFEntryIgnored(t *testing.T) {
	// Attacker sends "X-Forwarded-For: 6.6.6.6"; the trusted proxy appends the real
	// client (9.9.9.9). Rightmost-untrusted wins, so the spoofed 6.6.6.6 is ignored.
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}, "6.6.6.6, 9.9.9.9"))
}

func TestClientIP_RepeatedXFFHeadersAreOrdered(t *testing.T) {
	// A repeated header is equivalent to one comma-separated list, so the client is
	// still the right-most untrusted entry across all header values.
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}, "6.6.6.6", "9.9.9.9, 10.0.0.2"))
}

func TestClientIP_AllHopsTrustedFallsBackToPeer(t *testing.T) {
	assert.Equal(t, "10.0.0.1", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}, "10.0.0.2, 10.0.0.3"))
}

func TestClientIP_TrustedPeerNoXFFUsesPeer(t *testing.T) {
	assert.Equal(t, "10.0.0.1", resolvedIP(t, "10.0.0.1:5555", []string{"10.0.0.0/8"}))
}

func TestClientIP_TrustedProxyByBareIP(t *testing.T) {
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "192.168.1.1:5555", []string{"192.168.1.1"}, "9.9.9.9"))
}

func TestClientIP_IPv6TrustedProxy(t *testing.T) {
	assert.Equal(t, "9.9.9.9", resolvedIP(t, "[2001:db8::1]:5555", []string{"2001:db8::/32"}, "9.9.9.9"))
}

// Without trusted proxies the legacy behavior is kept: all X-Forwarded-For
// entries are candidate client IPs.

func TestLegacy_AllXFFEntriesCollected(t *testing.T) {
	assert.ElementsMatch(
		t,
		[]string{"6.6.6.6", "9.9.9.9"},
		resolvedIPs(t, "10.0.0.1:5555", nil, "6.6.6.6, 9.9.9.9"),
	)
}

func TestLegacy_RepeatedXFFHeadersCollected(t *testing.T) {
	assert.ElementsMatch(
		t,
		[]string{"6.6.6.6", "9.9.9.9", "10.0.0.2"},
		resolvedIPs(t, "10.0.0.1:5555", nil, "6.6.6.6", "9.9.9.9, 10.0.0.2"),
	)
}

func TestLegacy_NoXFFUsesPeer(t *testing.T) {
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4:5555", nil))
}

func TestLegacy_EmptyXFFUsesPeer(t *testing.T) {
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4:5555", nil, ""))
}

// RemoteAddr handling.

func TestRemoteAddr_EmptyDefaultsToLocalhost(t *testing.T) {
	assert.Equal(t, "127.0.0.1", resolvedIP(t, "", nil))
}

func TestRemoteAddr_UnparseableDefaultsToLocalhost(t *testing.T) {
	assert.Equal(t, "127.0.0.1", resolvedIP(t, "not-an-address", nil))
}

func TestRemoteAddr_NonIPHostDefaultsToLocalhost(t *testing.T) {
	assert.Equal(t, "127.0.0.1", resolvedIP(t, "example.com:5555", nil))
}

func TestRemoteAddr_BareIP(t *testing.T) {
	assert.Equal(t, "1.2.3.4", resolvedIP(t, "1.2.3.4", nil))
}

func TestRemoteAddr_IPv6(t *testing.T) {
	assert.Equal(t, "2001:db8::1", resolvedIP(t, "[2001:db8::1]:5555", nil))
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
	t.Run("bare ipv6 becomes /128", func(t *testing.T) {
		p, err := utils.ParseTrustedProxies([]string{"2001:db8::1"})
		require.NoError(t, err)
		require.Len(t, p, 1)
		assert.Equal(t, 128, p[0].Bits())
	})
	t.Run("blank entries skipped", func(t *testing.T) {
		p, err := utils.ParseTrustedProxies([]string{"", "  "})
		require.NoError(t, err)
		assert.Empty(t, p)
	})
	t.Run("invalid ip returns error", func(t *testing.T) {
		_, err := utils.ParseTrustedProxies([]string{"not-an-ip"})
		assert.ErrorContains(t, err, `invalid trusted proxy IP "not-an-ip"`)
	})
	t.Run("invalid cidr returns error", func(t *testing.T) {
		_, err := utils.ParseTrustedProxies([]string{"10.0.0.0/99"})
		assert.ErrorContains(t, err, `invalid trusted proxy CIDR "10.0.0.0/99"`)
	})
}
