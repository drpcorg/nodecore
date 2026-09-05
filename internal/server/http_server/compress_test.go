package http_server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var compressBody = []byte(`{"jsonrpc":"2.0","id":1,"result":"0x1010101010101010101010"}`)

func serveCompressed(t *testing.T, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	e.Use(http_server.Compress())
	e.GET("/", func(c echo.Context) error {
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, compressBody)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		req.Header.Set(echo.HeaderAcceptEncoding, acceptEncoding)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestCompressServesTheNegotiatedCoding(t *testing.T) {
	tests := []struct {
		name            string
		acceptEncoding  string
		contentEncoding string
	}{
		{"zstd client", "zstd", "zstd"},
		{"gzip client", "gzip", "gzip"},
		{"client offering both prefers zstd", "gzip, zstd", "zstd"},
		{"client refusing zstd still gets gzip", "zstd;q=0, gzip", "gzip"},
		{"unknown coding is not compressed", "br", ""},
		{"no header is not compressed", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			rec := serveCompressed(te, tt.acceptEncoding)

			require.Equal(te, http.StatusOK, rec.Code)
			assert.Equal(te, tt.contentEncoding, rec.Header().Get(echo.HeaderContentEncoding))

			reader, err := compression.WrapReader(tt.contentEncoding, bytes.NewReader(rec.Body.Bytes()))
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()
			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Equal(te, compressBody, got, "the client must be able to decode what it asked for")
		})
	}
}

// Caches key on Accept-Encoding or they hand a zstd body to a gzip-only
// client, so the header is announced whether or not this response was
// compressed.
func TestCompressAlwaysVariesOnAcceptEncoding(t *testing.T) {
	for _, acceptEncoding := range []string{"", "gzip", "zstd"} {
		rec := serveCompressed(t, acceptEncoding)

		assert.Contains(t, rec.Header().Values(echo.HeaderVary), echo.HeaderAcceptEncoding)
	}
}

// A handler that writes no body must not leave a Content-Encoding behind,
// or the client tries to decode zero bytes as a compressed frame.
func TestCompressLeavesEmptyResponsesUnencoded(t *testing.T) {
	e := echo.New()
	e.Use(http_server.Compress())
	e.GET("/", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, "zstd")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding))
	assert.Empty(t, rec.Body.Bytes())
}
