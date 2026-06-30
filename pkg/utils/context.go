package utils

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
)

type contextKey string

const (
	ipKey contextKey = "ip"
)

// ParseTrustedProxies converts a list of CIDRs or bare IPs into prefixes. A bare
// IP becomes a host prefix (/32 or /128). Blank entries are skipped; an
// unparseable entry is an error so misconfiguration fails loudly at startup.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.Contains(e, "/") {
			p, err := netip.ParsePrefix(e)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", e, err)
			}
			prefixes = append(prefixes, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy IP %q: %w", e, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// ContextWithIps resolves the single client IP for the request and stores it in
// the context. The client IP is the direct peer (RemoteAddr) unless that peer is
// a configured trusted proxy, in which case X-Forwarded-For is consulted: walking
// right-to-left, the first address that is not itself a trusted proxy is the
// client. When the peer is untrusted, X-Forwarded-For is ignored entirely, so a
// client connecting directly cannot spoof its IP by sending the header.
func ContextWithIps(ctx context.Context, request *http.Request, trustedProxies []netip.Prefix) context.Context {
	ipValues := mapset.NewThreadUnsafeSet[string]()
	ipValues.Add(clientIP(request, trustedProxies))
	return context.WithValue(ctx, ipKey, ipValues)
}

func clientIP(request *http.Request, trustedProxies []netip.Prefix) string {
	peer := remoteIP(request.RemoteAddr)
	if !isTrustedProxy(peer, trustedProxies) {
		return peer
	}
	// The peer is a trusted proxy: take the right-most X-Forwarded-For entry that
	// is not itself a trusted proxy (the proxy appends the connecting IP on the
	// right, so trusted hops are skipped from the right).
	if xff := request.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(parts[i])
			if ip == "" {
				continue
			}
			if !isTrustedProxy(ip, trustedProxies) {
				return ip
			}
		}
	}
	// No untrusted forwarded entry (header absent or every hop trusted): the best
	// available identity is the trusted peer itself.
	return peer
}

func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	if remoteAddr != "" {
		return remoteAddr
	}
	return "127.0.0.1"
}

func isTrustedProxy(ip string, trustedProxies []netip.Prefix) bool {
	if len(trustedProxies) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, p := range trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func IpsFromContext(ctx context.Context) mapset.Set[string] {
	ips, ok := ctx.Value(ipKey).(mapset.Set[string])
	if !ok {
		return nil
	}
	return ips
}
