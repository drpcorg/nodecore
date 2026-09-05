package compression_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zstdBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	_, err = w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestWrapReaderDecodesSupportedCodings(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{"gzip", "gzip", gzipBytes(t, plain)},
		{"zstd", "zstd", zstdBytes(t, plain)},
		{"case-insensitive", "ZSTD", zstdBytes(t, plain)},
		{"no encoding is passed through", "", plain},
		{"identity is passed through", "identity", plain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			reader, err := compression.WrapReader(tt.contentEncoding, bytes.NewReader(tt.body))
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()

			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

// An upstream answering a coding nodecore never offered must be reported, not
// passed on: the body would reach the client as bytes it cannot read, and the
// connector strips Content-Encoding so it would not even know why.
func TestWrapReaderRejectsUnsupportedCodings(t *testing.T) {
	for _, contentEncoding := range []string{"br", "deflate", "gzip, gzip"} {
		t.Run(contentEncoding, func(te *testing.T) {
			_, err := compression.WrapReader(contentEncoding, bytes.NewReader(nil))

			assert.ErrorIs(te, err, compression.ErrUnsupportedEncoding)
		})
	}
}

// Codecs are pooled, so a reader returned by Close must come back clean:
// a decoder still holding the previous stream's state produces garbage on
// its next use.
func TestWrapReaderIsReusableAfterClose(t *testing.T) {
	for _, scheme := range []string{"gzip", "zstd"} {
		t.Run(scheme, func(te *testing.T) {
			for _, plain := range [][]byte{[]byte("first body"), []byte("a completely different second body")} {
				var body []byte
				if scheme == "gzip" {
					body = gzipBytes(te, plain)
				} else {
					body = zstdBytes(te, plain)
				}

				reader, err := compression.WrapReader(scheme, bytes.NewReader(body))
				require.NoError(te, err)
				got, err := io.ReadAll(reader)
				require.NoError(te, err)
				require.NoError(te, reader.Close())

				assert.Equal(te, plain, got)
			}
		})
	}
}

// A truncated frame is a broken upstream, not a panic: the error must reach
// the caller so the request fails cleanly.
func TestWrapReaderReportsCorruptBody(t *testing.T) {
	truncated := zstdBytes(t, bytes.Repeat([]byte("x"), 1024))[:20]

	reader, err := compression.WrapReader("zstd", bytes.NewReader(truncated))
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	_, err = io.ReadAll(reader)

	assert.Error(t, err)
}

// A frame that does not start with zstd's magic number is not zstd at all,
// and saying so at wrap time turns a peer's mislabelled body into a clean
// rejection instead of an error surfacing mid-read from somewhere deeper.
func TestWrapReaderRejectsBodyThatIsNotZstd(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"plain json", []byte(`{"jsonrpc":"2.0","id":1}`)},
		{"gzip bytes under a zstd label", gzipBytes(t, []byte("hello"))},
		{"magic truncated", zstdBytes(t, []byte("hello"))[:2]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			_, err := compression.WrapReader("zstd", bytes.NewReader(tt.body))

			assert.Error(te, err)
			assert.NotErrorIs(te, err, compression.ErrUnsupportedEncoding,
				"the coding is supported; it is this body that is wrong")
		})
	}
}

// An empty body is a legitimate zero-byte payload, not a malformed frame.
func TestWrapReaderAcceptsEmptyZstdBody(t *testing.T) {
	reader, err := compression.WrapReader("zstd", bytes.NewReader(nil))
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	got, err := io.ReadAll(reader)

	require.NoError(t, err)
	assert.Empty(t, got)
}
