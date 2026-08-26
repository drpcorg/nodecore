package http_server

// the package is adapted from echo's decompress middleware
// https://github.com/labstack/echo/blob/master/middleware/decompress.go
// which only ever understood gzip.

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
)

// Decompress returns a middleware that decodes a compressed request body, so
// handlers always read plain bytes whatever the client sent. Codings nodecore
// does not speak are passed through untouched rather than guessed at.
func Decompress() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			encoding := strings.TrimSpace(req.Header.Get(echo.HeaderContentEncoding))
			if encoding == "" || strings.EqualFold(encoding, "identity") {
				return next(c)
			}

			reader, err := compression.WrapReader(encoding, req.Body)
			if errors.Is(err, compression.ErrUnsupportedEncoding) {
				return next(c)
			}
			if err != nil {
				zerolog.Ctx(req.Context()).Debug().Err(err).Msg("client sent an undecodable request body")
				return echo.NewHTTPError(http.StatusBadRequest, "invalid compressed request body")
			}
			// Releasing the codec here rather than through req.Body keeps it
			// out of reach of the server's own Close of the original body,
			// which owns the connection and must stay the one to close it.
			defer func() { _ = reader.Close() }()
			req.Body = io.NopCloser(reader)

			// The header described the bytes that arrived, not the ones the
			// handler now reads. Left in place it would be forwarded to an
			// upstream and tell a node to decompress a plain body.
			req.Header.Del(echo.HeaderContentEncoding)

			return next(c)
		}
	}
}
