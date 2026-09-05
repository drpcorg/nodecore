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
func Negotiate(acceptEncoding string) Scheme {
	if acceptEncoding == "" {
		return Identity
	}

	best, bestQ := Identity, 0.0
	for _, part := range strings.Split(acceptEncoding, ",") {
		coding, quality := parseCoding(part)
		if quality == 0 {
			continue
		}
		switch coding {
		case "zstd", "*":
			coding = string(Zstd)
		case "gzip":
			// keep
		default:
			continue
		}
		// A strictly higher q always wins; an equal q only promotes zstd, so
		// the tie-break can never demote a coding the client ranked higher.
		if quality > bestQ || (quality == bestQ && Scheme(coding) == Zstd) {
			best, bestQ = Scheme(coding), quality
		}
	}
	return best
}

// parseCoding splits one Accept-Encoding element into its coding name and its
// q value. A missing or malformed q means q=1: a client that garbled the
// parameter still asked for the coding, and treating that as a refusal would
// silently drop compression instead of failing loudly.
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
		if err != nil {
			return name, 1
		}
		return name, quality
	}
	return name, 1
}
