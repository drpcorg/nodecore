// Package compression implements the HTTP content codings nodecore speaks on
// both of its edges: the client-facing ingress and the upstream connectors.
//
// Both edges need the same three things - decide which coding to use, encode
// a body, decode a body - so the codec pools live here once rather than in
// each edge. Levels are fixed at the fastest setting of each codec: a proxy
// pays the compression cost on the hot path of every request, where CPU time
// costs more than the extra few percent of ratio.
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
)

// Offer is the Accept-Encoding nodecore sends upstream. zstd leads on
// preference, but a node that knows neither simply answers identity - content
// negotiation degrades on its own, which is why this needs no config knob.
const Offer = "zstd, gzip"

// Negotiate picks the coding to encode a response with, given the client's
// Accept-Encoding (RFC 9110 §12.5.3). The highest q wins; zstd breaks a tie
// because it decodes faster and compresses denser than gzip at these levels.
// Anything unrecognised, refused with q=0, or absent yields Identity.
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
	var gzipQ, zstdQ, identityQ, wildcardQ float64
	var gzipNamed, zstdNamed, identityNamed, wildcardNamed bool

	for _, part := range strings.Split(acceptEncoding, ",") {
		coding, quality := parseCoding(part)
		switch coding {
		case string(Gzip):
			gzipQ, gzipNamed = quality, true
		case string(Zstd):
			zstdQ, zstdNamed = quality, true
		case "identity":
			identityQ, identityNamed = quality, true
		case "*":
			wildcardQ, wildcardNamed = quality, true
		}
	}
	if wildcardNamed {
		// A bare "*" is a client saying anything is acceptable, so it reaches
		// both codings at the same q and the tie-break below picks zstd.
		if !gzipNamed {
			gzipQ = wildcardQ
		}
		if !zstdNamed {
			zstdQ = wildcardQ
		}
	}

	// A client that ranks plain bytes above every coding it offered is asking
	// not to be compressed, and only an explicit "identity" says that - left
	// unmentioned it is the fallback, which it stays either way. The
	// comparison is strict so an unranked tie still compresses.
	if identityNamed && identityQ > gzipQ && identityQ > zstdQ {
		return Identity
	}

	// A q of zero is a refusal rather than a weak preference, so it never
	// wins. Of what remains the higher q takes it, and zstd takes an exact
	// tie - which can never demote a coding the client ranked higher.
	switch {
	case zstdQ > 0 && zstdQ >= gzipQ:
		return Zstd
	case gzipQ > 0:
		return Gzip
	default:
		return Identity
	}
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
