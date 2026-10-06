package compression_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/stretchr/testify/assert"
)

// Negotiate picks the response coding from a client's Accept-Encoding.
// Highest q wins; an exact tie goes to zstd, then br, then gzip - zstd is the
// densest and fastest to decode, and br beats gzip on size and CPU on the
// bodies large enough for either to matter.
func TestNegotiate(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding string
		expected       compression.Scheme
	}{
		{"no header means no compression", "", compression.Identity},
		{"gzip only", "gzip", compression.Gzip},
		{"zstd only", "zstd", compression.Zstd},
		{"both offered, zstd wins the tie", "gzip, zstd", compression.Zstd},
		{"both offered, order does not matter", "zstd, gzip", compression.Zstd},
		{"zstd explicitly refused", "zstd;q=0, gzip", compression.Gzip},
		{"higher q wins over the tie-break", "gzip;q=0.5, zstd;q=0.1", compression.Gzip},
		{"unsupported codings are ignored", "deflate, compress", compression.Identity},
		{"wildcard offers everything", "*", compression.Zstd},
		{"identity is not a compression", "identity", compression.Identity},
		{"coding names are case-insensitive", "GZIP", compression.Gzip},
		{"everything refused", "zstd;q=0, gzip;q=0", compression.Identity},
		{"malformed q is treated as acceptable", "gzip;q=abc", compression.Gzip},
		{"whitespace around parameters", " zstd ; q=0.9 , gzip ", compression.Gzip},
		// RFC 9110 §12.5.3: "*" matches only the codings the client did not
		// name. A coding named with q=0 is refused, and the wildcard does not
		// speak for it - which matters most for exactly this client, the one
		// that cannot read what it just refused.
		{"the wildcard does not override an explicit refusal", "zstd;q=0, *", compression.Brotli},
		{"nor does the order it appears in change that", "*, zstd;q=0", compression.Brotli},
		{"a named coding outranks the wildcard on q", "gzip;q=1.0, *;q=0.5", compression.Gzip},
		{"the wildcard still carries the codings nobody named", "*;q=0.5", compression.Zstd},
		{"everything refused through the wildcard", "*;q=0", compression.Identity},
		{"a coding named twice is settled by its last mention", "gzip, gzip;q=0", compression.Identity},
		{"browsers offering all four", "gzip, deflate, br, zstd", compression.Zstd},
		{"the legacy refuse-everything-else header", "gzip;q=1.0, identity;q=0.5, *;q=0", compression.Gzip},
		{"a negative q is nonsense, not a preference", "gzip;q=-1", compression.Identity},
		{"q is found even when it is not the first parameter", "gzip;a=b;q=0", compression.Identity},
		// qvalue is defined over [0,1]; ParseFloat is not, and an out-of-range
		// or non-finite q must not be able to outrank a real preference.
		{"a q above 1 cannot outrank a real preference", "gzip;q=1e9, zstd", compression.Zstd},
		{"an infinite q cannot outrank a real preference", "gzip;q=+Inf, zstd", compression.Zstd},
		{"NaN is malformed, not a refusal", "gzip;q=NaN", compression.Gzip},
		// identity is the fallback whether or not it is named, but a client
		// that ranks it above every coding it offered is asking for plain
		// bytes and gets them.
		{"identity preferred over the codings offered", "identity;q=1, gzip;q=0.5", compression.Identity},
		{"identity ranked below a coding does not win", "identity;q=0.5, gzip;q=1", compression.Gzip},
		{"an unranked tie still compresses", "identity, gzip", compression.Gzip},
		{"identity named but refused", "identity;q=0, gzip", compression.Gzip},
		{"nothing is acceptable at all", "identity;q=0", compression.Identity},
		// brotli sits between the two: it never displaces zstd on a tie, and
		// always displaces gzip on one.
		{"brotli only", "br", compression.Brotli},
		{"brotli beats gzip on a tie", "br, gzip", compression.Brotli},
		{"brotli beats gzip whatever the client's order", "gzip, br", compression.Brotli},
		{"a browser without zstd gets brotli", "gzip, deflate, br", compression.Brotli},
		{"brotli never displaces zstd on a tie", "br, zstd", compression.Zstd},
		{"brotli never displaces zstd whatever the client's order", "zstd, br", compression.Zstd},
		{"brotli wins on a higher q than zstd", "br;q=0.9, zstd;q=0.5", compression.Brotli},
		{"brotli loses on a lower q than gzip", "br;q=0.1, gzip", compression.Gzip},
		{"brotli explicitly refused", "br;q=0, gzip", compression.Gzip},
		{"the wildcard still reaches zstd when brotli is refused", "br;q=0, *", compression.Zstd},
		{"refusing zstd and brotli leaves gzip", "zstd;q=0, br;q=0, *", compression.Gzip},
		{"brotli matching is case-insensitive", "BR", compression.Brotli},
		{"brotli named twice is settled by its last mention", "br, br;q=0", compression.Identity},
		{"brotli's token is br, not brotli", "brotli", compression.Identity},
		{"identity ranked below brotli does not win", "identity;q=0.5, br", compression.Brotli},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			assert.Equal(te, tt.expected, compression.Negotiate(tt.acceptEncoding))
		})
	}
}

// Offer lists every coding in the order nodecore prefers them, so a node that
// breaks ties by the client's order lands where Negotiate would.
func TestOfferListsEveryCodingInPreferenceOrder(t *testing.T) {
	assert.Equal(t, "zstd, br, gzip", compression.Offer)
	assert.Equal(t, compression.Zstd, compression.Negotiate(compression.Offer))
}
