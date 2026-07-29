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

	localhostIP = "127.0.0.1"
)

// ParseTrustedProxies converts a list of CIDRs or bare IPs into prefixes. A bare
// IP becomes a host prefix (/32 or /128). Blank entries are skipped; an
// unparseable entry is an error so misconfiguration fails loudly at startup. The
// result is nil when nothing meaningful was configured.
func ParseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
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

// ContextWithIps resolves the client IPs of the request and stores them in the
// context.
//
// With trusted proxies configured, exactly one IP is resolved: the direct peer
// (RemoteAddr) unless that peer is a trusted proxy, in which case
// X-Forwarded-For is consulted - walking right-to-left, the first address that
// is not itself a trusted proxy is the client. When the peer is untrusted,
// X-Forwarded-For is ignored entirely, so a client connecting directly cannot
// spoof its IP by sending the header.
//
// Without trusted proxies (the default) the legacy behavior is kept for
// backwards compatibility: every X-Forwarded-For entry is treated as a candidate
// client IP, falling back to the direct peer when the header is absent. Note
// that in this mode a directly connected client can present an arbitrary IP.
func ContextWithIps(ctx context.Context, request *http.Request, trustedProxies []netip.Prefix) context.Context {
	ipValues := mapset.NewThreadUnsafeSet[string]()
	if len(trustedProxies) == 0 {
		for _, ip := range forwardedForIPs(request) {
			ipValues.Add(ip)
		}
		if ipValues.IsEmpty() {
			ipValues.Add(remoteIP(request.RemoteAddr))
		}
	} else {
		ipValues.Add(clientIP(request, trustedProxies))
	}
	return context.WithValue(ctx, ipKey, ipValues)
}

func clientIP(request *http.Request, trustedProxies []netip.Prefix) string {
	peer := remoteIP(request.RemoteAddr)
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !isTrustedProxy(peerAddr, trustedProxies) {
		return peer
	}
	// The peer is a trusted proxy: take the right-most X-Forwarded-For entry that
	// is not itself a trusted proxy (the proxy appends the connecting IP on the
	// right, so trusted hops are skipped from the right).
	forwarded := forwardedForIPs(request)
	for i := len(forwarded) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(forwarded[i])
		if err != nil {
			// A malformed hop carries no usable identity, and treating it as the
			// client would let anything upstream of a trusted proxy inject garbage.
			// Skip it and keep walking left.
			continue
		}
		if !isTrustedProxy(addr, trustedProxies) {
			return forwarded[i]
		}
	}
	// No untrusted forwarded entry (header absent, every hop trusted or malformed):
	// the best available identity is the trusted peer itself.
	return peer
}

// forwardedForIPs returns the X-Forwarded-For entries in wire order, left to
// right. A request may carry the header several times, so all of its values are
// flattened - net/http keeps them as separate entries and Header.Get would only
// see the first one.
func forwardedForIPs(request *http.Request) []string {
	values := request.Header.Values("X-Forwarded-For")
	ips := make([]string, 0, len(values))
	for _, value := range values {
		for _, ip := range strings.Split(value, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

// remoteIP extracts the peer IP from a RemoteAddr. An address that is neither
// host:port nor a bare IP falls back to 127.0.0.1 so that an unparseable value
// never leaks into IP checks.
func remoteIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if _, err := netip.ParseAddr(host); err == nil {
			return host
		}
		return localhostIP
	}
	if _, err := netip.ParseAddr(remoteAddr); err == nil {
		return remoteAddr
	}
	return localhostIP
}

func isTrustedProxy(addr netip.Addr, trustedProxies []netip.Prefix) bool {
	if len(trustedProxies) == 0 {
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
