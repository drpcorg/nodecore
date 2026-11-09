// Package compression implements the HTTP content codings nodecore speaks on
// both of its edges: the client-facing ingress and the upstream connectors.
//
// Both edges need the same three things - decide which coding to use, encode
// a body, decode a body - so the codec pools live here once rather than in
// each edge. Levels are fixed at the cheapest useful setting of each codec: a
// proxy pays the compression cost on the hot path of every request, where CPU
// time costs more than the extra few percent of ratio.
package compression

import (
	"math"
	"strconv"
	"strings"
)

// Scheme is a content coding nodecore can encode and decode.
type Scheme string

const (
	// Identity means "no compression"; it is the zero value so an
	// unparseable or absent Accept-Encoding degrades to plain bodies.
	Identity Scheme = ""
	Gzip     Scheme = "gzip"
	Zstd     Scheme = "zstd"
	// Brotli's content-coding token is "br" (RFC 7932), not "brotli": a
	// client asking for the latter is asking for a coding nobody implements.
	Brotli Scheme = "br"
)

// preference is the order an exact tie in Accept-Encoding is settled in. zstd
// is the densest of the three at these levels and the fastest for a client to
// decode; brotli beats gzip on both size and CPU on the bodies large enough
// for either to matter; gzip is the one every client reads.
var preference = [...]Scheme{Zstd, Brotli, Gzip}

// Offer is the Accept-Encoding nodecore sends upstream, in preference order.
// A node that knows none of them simply answers identity - content
// negotiation degrades on its own, which is why this needs no config knob.
const Offer = "zstd, br, gzip"

// Negotiate picks the coding to encode a response with, given the client's
// Accept-Encoding (RFC 9110 §12.5.3). The highest q wins, and an exact tie
// goes to whichever coding comes first in preference. Anything unrecognised,
// refused with q=0, or absent yields Identity.
//
// A coding the client names twice is settled by its last mention, which the
// RFC leaves open and which lets a merged header ("gzip, gzip;q=0", two field
// lines joined) express a refusal rather than contradict itself.
//
// When nothing at all is acceptable - "identity;q=0" on its own, or "*;q=0" -
// this answers Identity rather than the 406 §12.5.3 permits. Serving a plain
// body is the response an RPC client can actually use, and the RFC allows
// disregarding negotiation when no available representation is acceptable.
func Negotiate(acceptEncoding string) Scheme {
	if acceptEncoding == "" {
		return Identity
	}

	// "*" stands in only for the codings the client did not name itself, so
	// its q is collected separately and applied afterwards. Folding it in
	// during the scan is how "zstd;q=0, *" would end up serving zstd to a
	// client that had just refused it.
	var quality [len(preference)]float64
	var named [len(preference)]bool
	var identityQ, wildcardQ float64
	var identityNamed, wildcardNamed bool

	for _, part := range strings.Split(acceptEncoding, ",") {
		coding, q := parseCoding(part)
		switch coding {
		case "identity":
			identityQ, identityNamed = q, true
		case "*":
			wildcardQ, wildcardNamed = q, true
		default:
			for i, scheme := range preference {
				if coding == string(scheme) {
					quality[i], named[i] = q, true
					break
				}
			}
		}
	}

	// A q of zero is a refusal rather than a weak preference, so it never
	// wins: nothing displaces Identity without a q above zero. Only a
	// strictly higher q displaces the best so far, which is what hands an
	// exact tie to the coding earlier in preference - and that can never
	// demote a coding the client ranked higher.
	best, bestQ := Identity, 0.0
	for i, scheme := range preference {
		q := quality[i]
		if !named[i] {
			if !wildcardNamed {
				continue
			}
			// A bare "*" is a client saying anything is acceptable, so it
			// reaches every coding the client did not name, at the same q.
			q = wildcardQ
		}
		if q > bestQ {
			best, bestQ = scheme, q
		}
	}

	// A client that ranks plain bytes above every coding it offered is asking
	// not to be compressed, and only an explicit "identity" says that - left
	// unmentioned it is the fallback, which it stays either way. The
	// comparison is strict so an unranked tie still compresses.
	if identityNamed && identityQ > bestQ {
		return Identity
	}
	return best
}

// parseCoding splits one Accept-Encoding element into its coding name and its
// q value. A missing or malformed q means q=1: a client that garbled the
// parameter still asked for the coding, and treating that as a refusal would
// silently drop compression instead of failing loudly.
//
// The q that comes back is always a real number in [0,1]. RFC 9110 §12.4.2
// defines qvalue over exactly that range, but ParseFloat is happy to hand
// back "1e9", "+Inf" or "NaN", and those would order themselves against real
// preferences in ways nobody meant.
func parseCoding(part string) (string, float64) {
	name, params, hasParams := strings.Cut(part, ";")
	name = strings.ToLower(strings.TrimSpace(name))
	if !hasParams {
		return name, 1
	}
	for _, param := range strings.Split(params, ";") {
		key, value, ok := strings.Cut(param, "=")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "q" {
			continue
		}
		quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		switch {
		case err != nil, math.IsNaN(quality):
			return name, 1
		case quality < 0:
			return name, 0
		case quality > 1:
			return name, 1
		}
		return name, quality
	}
	return name, 1
}
