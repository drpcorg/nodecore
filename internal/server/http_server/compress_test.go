package http_server_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/drpcorg/nodecore/internal/server/http_server"
	"github.com/gorilla/websocket"
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

// A handler that writes and then fails hands echo an error after this
// middleware has already closed the body and returned the codec to the pool.
// Nothing may be written through that codec afterwards: it belongs to
// whichever response takes it out of the pool next.
func TestCompressRefusesWritesAfterTheBodyIsFinished(t *testing.T) {
	var stolen bytes.Buffer
	var lateErr error

	e := echo.New()
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		// Stand in for a concurrent response acquiring the encoder that the
		// middleware has just released.
		writer, acquireErr := compression.AcquireWriter(compression.Zstd, &stolen)
		require.NoError(t, acquireErr)
		defer compression.ReleaseWriter(writer)
		_, lateErr = c.Response().Write([]byte("LATE-ERROR-BODY"))
		require.NoError(t, writer.Close())
	}
	e.Use(http_server.Compress())
	e.GET("/", func(c echo.Context) error {
		if _, err := c.Response().Write([]byte("partial")); err != nil {
			return err
		}
		return errors.New("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, "zstd")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Error(t, lateErr, "a write with nowhere to go must say so rather than vanish")

	// The other response's stream has to be decoded before it can be judged:
	// looking for a plaintext marker in compressed bytes proves nothing either
	// way, since the codec may or may not have stored that run literally.
	stolenReader, err := compression.WrapReader("zstd", bytes.NewReader(stolen.Bytes()))
	require.NoError(t, err)
	defer func() { require.NoError(t, stolenReader.Close()) }()
	stolenPlain, err := io.ReadAll(stolenReader)
	require.NoError(t, err)
	assert.NotContains(t, string(stolenPlain), "LATE-ERROR-BODY",
		"the late write reached an encoder that had already been handed to another response")
	assert.Empty(t, stolenPlain, "that response wrote nothing of its own, so it must decode to nothing")

	reader, err := compression.WrapReader("zstd", bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, []byte("partial"), got, "and it must not corrupt this response either")
}

// A handler that fails before writing anything never labelled the response
// with a coding, so the error body echo writes afterwards has to reach the
// client as plain, readable bytes.
func TestCompressServesAnErrorRaisedBeforeAnyWrite(t *testing.T) {
	e := echo.New()
	e.Use(http_server.Compress())
	e.GET("/", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusBadGateway, "upstream is unhappy")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, "zstd")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Empty(t, rec.Header().Get(echo.HeaderContentEncoding))
	assert.Contains(t, rec.Body.String(), "upstream is unhappy")
}

// A WebSocket handshake that gorilla rejects writes its own response through
// c.Response().Writer, which never sets echo's Committed flag - so echo's
// default error handler runs afterwards and writes again, through a writer
// whose encoder this middleware has already returned to the pool. The client
// gets gorilla's rejection, decodable and complete, and nothing reaches the
// pooled encoder.
func TestCompressSurvivesARejectedWebsocketUpgrade(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var stolen bytes.Buffer

	e := echo.New()
	defaultErrorHandler := e.HTTPErrorHandler
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		// Echo's own handler is what writes here, and it does so because
		// gorilla wrote through c.Response().Writer and left Committed false.
		// Standing in for a concurrent response that acquired the encoder this
		// middleware has just released is what makes the write observable.
		writer, acquireErr := compression.AcquireWriter(compression.Zstd, &stolen)
		require.NoError(t, acquireErr)
		defer compression.ReleaseWriter(writer)
		defaultErrorHandler(err, c)
		require.NoError(t, writer.Close())
	}
	e.Use(http_server.Compress())
	e.GET("/", func(c echo.Context) error {
		conn, err := upgrader.Upgrade(c.Response().Writer, c.Request(), nil)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		return nil
	})

	srv := httptest.NewServer(e)
	defer srv.Close()

	// Upgrade: websocket with no Connection: Upgrade is exactly the handshake
	// gorilla turns down.
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set(echo.HeaderAcceptEncoding, "zstd")

	resp, err := (&http.Transport{}).RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	body := raw
	if encoding := resp.Header.Get(echo.HeaderContentEncoding); encoding != "" {
		reader, err := compression.WrapReader(encoding, bytes.NewReader(raw))
		require.NoError(t, err, "whatever went out has to be decodable")
		defer func() { require.NoError(t, reader.Close()) }()
		body, err = io.ReadAll(reader)
		require.NoError(t, err, "a truncated or double-written stream shows up here")
	}

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, string(body), "Bad Request")
	assert.NotContains(t, string(body), "Internal Server Error",
		"echo's error handler must not have appended a second body")

	stolenReader, err := compression.WrapReader("zstd", bytes.NewReader(stolen.Bytes()))
	require.NoError(t, err)
	defer func() { require.NoError(t, stolenReader.Close()) }()
	stolenPlain, err := io.ReadAll(stolenReader)
	require.NoError(t, err)
	assert.Empty(t, stolenPlain,
		"echo's error body reached an encoder that had already been handed to another response")
}

// Accept-Encoding may arrive as several field lines. They mean the same as one
// comma-separated line (RFC 9110 §5.3), so reading only the first would lose
// whatever the others said - including a refusal.
func TestCompressReadsEveryAcceptEncodingFieldLine(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		expected string
	}{
		{"a refusal on a later line still counts", []string{"zstd;q=0", "gzip"}, "gzip"},
		{"a coding on a later line is still offered", []string{"br", "zstd"}, "zstd"},
		{"split across lines the way a proxy might", []string{"gzip;q=0.5", "zstd;q=0.1"}, "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			e := echo.New()
			e.Use(http_server.Compress())
			e.GET("/", func(c echo.Context) error {
				return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, compressBody)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for _, line := range tt.lines {
				req.Header.Add(echo.HeaderAcceptEncoding, line)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			require.Equal(te, http.StatusOK, rec.Code)
			assert.Equal(te, tt.expected, rec.Header().Get(echo.HeaderContentEncoding))
			assert.Equal(te, compressBody, decodeBody(te, tt.expected, rec.Body.Bytes()))
		})
	}
}

func decodeBody(t *testing.T, contentEncoding string, raw []byte) []byte {
	t.Helper()
	reader, err := compression.WrapReader(contentEncoding, bytes.NewReader(raw))
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	return out
}
