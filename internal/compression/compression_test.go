package compression_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/stretchr/testify/assert"
)

// Negotiate picks the response coding from a client's Accept-Encoding.
// Highest q wins; zstd breaks a tie because it is both faster to decode
// and denser than gzip at comparable levels.
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
		{"unsupported codings are ignored", "br, deflate", compression.Identity},
		{"wildcard offers everything", "*", compression.Zstd},
		{"identity is not a compression", "identity", compression.Identity},
		{"coding names are case-insensitive", "GZIP", compression.Gzip},
		{"everything refused", "zstd;q=0, gzip;q=0", compression.Identity},
		{"malformed q is treated as acceptable", "gzip;q=abc", compression.Gzip},
		{"whitespace around parameters", " zstd ; q=0.9 , gzip ", compression.Gzip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			assert.Equal(te, tt.expected, compression.Negotiate(tt.acceptEncoding))
		})
	}
}
