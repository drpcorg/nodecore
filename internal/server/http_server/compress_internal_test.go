package http_server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compressedRequest runs inspect inside a handler behind the middleware and
// hands back the writer the middleware wrapped the response in, so a test can
// look at what it was holding during the handler and after it returned.
func compressedRequest(t *testing.T, acceptEncoding string, handle echo.HandlerFunc) (*compressResponseWriter, *httptest.ResponseRecorder) {
	t.Helper()
	var crw *compressResponseWriter
	e := echo.New()
	e.Use(Compress())
	e.GET("/", func(c echo.Context) error {
		var ok bool
		crw, ok = c.Response().Writer.(*compressResponseWriter)
		require.True(t, ok, "the middleware did not wrap the response writer")
		return handle(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderAcceptEncoding, acceptEncoding)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return crw, rec
}

// The encoder must not leave the pool until there is a body byte to put
// through it. A WebSocket handler hijacks the connection and then lives for as
// long as the socket does, so an encoder taken up front is pinned - unused,
// since a hijacked connection never goes through this writer - for the whole
// life of the connection. A pooled zstd encoder is over a megabyte of that.
func TestCompressTakesNoEncoderUntilTheFirstBodyByte(t *testing.T) {
	var duringHandler, afterFirstWrite any
	crw, rec := compressedRequest(t, "zstd", func(c echo.Context) error {
		duringHandler = c.Response().Writer.(*compressResponseWriter).writer
		_, err := c.Response().Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		afterFirstWrite = c.Response().Writer.(*compressResponseWriter).writer
		return err
	})

	assert.Nil(t, duringHandler, "an encoder was held for the whole handler")
	assert.NotNil(t, afterFirstWrite, "the first body byte must take an encoder")
	assert.Equal(t, "zstd", rec.Header().Get(echo.HeaderContentEncoding))
	assert.NotNil(t, crw)
}

// A response with no body never needs an encoder at all - a 204, a 304, and a
// WebSocket upgrade that hijacks the connection out from under this writer.
func TestCompressTakesNoEncoderForABodylessResponse(t *testing.T) {
	crw, rec := compressedRequest(t, "zstd", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	assert.Nil(t, crw.writer, "an encoder was taken for a response with no body")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding))
}
