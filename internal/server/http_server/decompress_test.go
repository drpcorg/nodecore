package http_server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	"github.com/klauspost/compress/zstd"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postCompressed sends body under contentEncoding and reports what the
// handler behind the middleware actually received.
func postCompressed(t *testing.T, contentEncoding string, body []byte) (*httptest.ResponseRecorder, []byte) {
	t.Helper()
	var seen []byte
	e := echo.New()
	e.Use(http_server.Decompress())
	e.POST("/", func(c echo.Context) error {
		read, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return err
		}
		seen = read
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	if contentEncoding != "" {
		req.Header.Set(echo.HeaderContentEncoding, contentEncoding)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec, seen
}

func compress(t *testing.T, scheme compression.Scheme, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer, err := compression.AcquireWriter(scheme, &buf)
	require.NoError(t, err)
	defer compression.ReleaseWriter(writer)
	_, err = writer.Write(plain)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func TestDecompressDecodesRequestBodies(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}`)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{"zstd", "zstd", compress(t, compression.Zstd, plain)},
		{"gzip", "gzip", compress(t, compression.Gzip, plain)},
		{"case-insensitive", "ZSTD", compress(t, compression.Zstd, plain)},
		{"no encoding", "", plain},
		{"identity", "identity", plain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			rec, seen := postCompressed(te, tt.contentEncoding, tt.body)

			require.Equal(te, http.StatusOK, rec.Code)
			assert.Equal(te, plain, seen)
		})
	}
}

// Once the body has been decoded the header no longer describes it. It has to
// go, or it rides along to the upstream and tells a node to decompress bytes
// that are already plain.
func TestDecompressDropsTheContentEncodingHeader(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}`)
	var seen string
	e := echo.New()
	e.Use(http_server.Decompress())
	e.POST("/", func(c echo.Context) error {
		seen = c.Request().Header.Get(echo.HeaderContentEncoding)
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(compress(t, compression.Zstd, plain)))
	req.Header.Set(echo.HeaderContentEncoding, "zstd")
	e.ServeHTTP(httptest.NewRecorder(), req)

	assert.Empty(t, seen)
}

// A coding nodecore does not decode is left alone rather than guessed at: the
// handler sees exactly the bytes the client sent, as it always has.
func TestDecompressPassesUnknownCodingsThrough(t *testing.T) {
	body := []byte("\x1b\x2f\x00 brotli-ish bytes")

	rec, seen := postCompressed(t, "br", body)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, body, seen)
}

// A client that declares a coding its body is not in gets a clean 400. This
// is a realistic client bug - a library that sets the header but forgets the
// encoder - and it must not read as a server fault.
func TestDecompressRejectsBodiesThatAreNotTheDeclaredCoding(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}`)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{"gzip declared, plain body", "gzip", plain},
		{"zstd declared, plain body", "zstd", plain},
		{"gzip declared, header truncated", "gzip", compress(t, compression.Gzip, plain)[:5]},
		{"zstd declared, magic truncated", "zstd", compress(t, compression.Zstd, plain)[:2]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			rec, _ := postCompressed(te, tt.contentEncoding, tt.body)

			assert.Equal(te, http.StatusBadRequest, rec.Code)
		})
	}
}

// A body that starts as valid but stops early cannot be caught before the
// handler reads it. What matters is that the request fails instead of being
// served as though the client had sent a short body.
func TestDecompressFailsOnTruncatedStream(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 4096)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{"zstd", "zstd", compress(t, compression.Zstd, plain)[:16]},
		{"gzip", "gzip", compress(t, compression.Gzip, plain)[:14]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			rec, seen := postCompressed(te, tt.contentEncoding, tt.body)

			assert.NotEqual(te, http.StatusOK, rec.Code)
			assert.NotEqual(te, plain, seen)
		})
	}
}

// An empty body is nothing to decode whatever it claims to be. echo's
// decompress middleware, which this replaced, made a point of letting one
// through ("ignore if body is empty"), and a client that sets the header on a
// bodyless POST must not start getting a 400 for it.
func TestDecompressPassesEmptyBodiesThrough(t *testing.T) {
	for _, contentEncoding := range []string{"gzip", "zstd", "identity", ""} {
		name := contentEncoding
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(te *testing.T) {
			rec, seen := postCompressed(te, contentEncoding, nil)

			assert.Equal(te, http.StatusOK, rec.Code)
			assert.Empty(te, seen)
		})
	}
}

// A client body decodes through the same pooled decoders as an upstream
// response, so the window cap that bounds a node's frame bounds a client's
// too. Without it a few hundred bytes on the wire commit tens of megabytes of
// heap, from anyone who can reach the port.
func TestDecompressRejectsFramesAboveTheWindowCap(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","method":"eth_blockNumber","id":1}`)
	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf, zstd.WithWindowSize(64<<20), zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	_, err = writer.Write(bytes.Repeat([]byte("padding"), 1<<20))
	require.NoError(t, err)
	_, err = writer.Write(plain)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	rec, seen := postCompressed(t, "zstd", buf.Bytes())

	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.NotEqual(t, plain, seen)
}

// A compressed request body is a size multiplier, and the multiplier is the
// sender's to choose: DEFLATE tops out near 1000:1, zstd has no such ceiling,
// so a few hundred kilobytes on the wire can ask nodecore to hold gigabytes.
// The decoded body is capped, and a body that runs past the cap fails rather
// than being quietly served short.
func TestDecompressCapsTheDecodedBodySize(t *testing.T) {
	oversize := http_server.MaxDecodedRequestBytes + 1<<20
	for _, scheme := range []compression.Scheme{compression.Zstd, compression.Gzip} {
		t.Run(string(scheme), func(te *testing.T) {
			bomb := compress(te, scheme, bytes.Repeat([]byte("A"), oversize))
			require.Less(te, len(bomb), 1<<20, "a bomb this cheap on the wire is the whole point of the cap")

			read, rec, readErr := postAndCount(te, string(scheme), bomb)

			require.Error(te, readErr, "the handler read a body past the cap without noticing")
			assert.LessOrEqual(te, read, int64(http_server.MaxDecodedRequestBytes))
			assert.NotEqual(te, http.StatusOK, rec.Code)
		})
	}
}

// The cap is a ceiling on abuse, not on real traffic: a body that decodes to
// exactly the cap is a body nodecore still has to serve intact.
func TestDecompressPassesBodiesUpToTheCap(t *testing.T) {
	plain := bytes.Repeat([]byte("A"), http_server.MaxDecodedRequestBytes)

	read, rec, readErr := postAndCount(t, "zstd", compress(t, compression.Zstd, plain))

	require.NoError(t, readErr)
	assert.Equal(t, int64(len(plain)), read)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// postAndCount reports how much of the body the handler managed to read, and
// the error it stopped on, without holding the decoded bytes in memory.
func postAndCount(t *testing.T, contentEncoding string, body []byte) (int64, *httptest.ResponseRecorder, error) {
	t.Helper()
	var read int64
	var readErr error
	e := echo.New()
	e.Use(http_server.Decompress())
	e.POST("/", func(c echo.Context) error {
		read, readErr = io.Copy(io.Discard, c.Request().Body)
		if readErr != nil {
			return readErr
		}
		return c.NoContent(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentEncoding, contentEncoding)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return read, rec, readErr
}
