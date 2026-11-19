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
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog"
)

// MaxDecodedRequestBytes caps how much a single request body may decode to.
//
// A compressed body is a size multiplier whose factor the sender chooses:
// DEFLATE tops out near 1000:1, zstd and brotli have no comparable ceiling, so
// without a cap a few hundred kilobytes on the wire can ask nodecore to hold
// gigabytes - from anyone who can reach the port. The window cap in
// internal/compression bounds what one frame header can make a decoder
// allocate; this bounds what the decoded bytes themselves can.
//
// 32MiB is far above anything JSON-RPC produces in practice - a batch of ten
// thousand calls is on the order of a megabyte - so it is a ceiling on abuse
// rather than a limit real traffic meets.
//
// It is the cap that applies when server.limits.max-request-size is not set.
// A configured limit takes its place - see Decompress.
const MaxDecodedRequestBytes = 32 << 20

// errRequestBodyTooLarge ends the read of a body that runs past its limit.
var errRequestBodyTooLarge = errors.New("the request body is too large")

// limitedReader fails the read that would take a body past limit instead of
// reporting EOF there, which is what io.LimitedReader does: a body handed over
// short is a body silently changed, and the handler would parse the truncation
// rather than reject it.
type limitedReader struct {
	reader    io.Reader
	remaining int64
	probe     [1]byte
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		// The cap has been handed over in full. One more byte settles whether
		// the body ended exactly on it or runs past it.
		n, err := l.reader.Read(l.probe[:])
		if n > 0 {
			return 0, errRequestBodyTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.reader.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// rejectedBody stands in for the body of a request that is turned away on its
// declared length: the handler's read fails at once and nothing is pulled off
// the wire.
type rejectedBody struct{}

func (rejectedBody) Read([]byte) (int, error) {
	return 0, errRequestBodyTooLarge
}

// Decompress returns a middleware that decodes a compressed request body, so
// handlers always read plain bytes whatever the client sent, and bounds how
// much of a body a handler may read. Codings nodecore does not speak are
// passed through untouched rather than guessed at.
//
// maxRequestBytes is server.limits.max-request-size. Zero means it is not set,
// and then only a body nodecore actually decodes is capped, at
// MaxDecodedRequestBytes - an undecoded body is bytes the client had to send
// in full, which is its own limit. A positive value bounds every body as the
// handler reads it, decoded or not, and replaces MaxDecodedRequestBytes for
// the decoded ones.
func Decompress(maxRequestBytes int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			encoding := strings.TrimSpace(req.Header.Get(echo.HeaderContentEncoding))
			if encoding == "" || strings.EqualFold(encoding, "identity") {
				limitUndecodedBody(req, maxRequestBytes)
				return next(c)
			}

			reader, err := compression.WrapReader(encoding, req.Body)
			if errors.Is(err, compression.ErrUnsupportedEncoding) {
				limitUndecodedBody(req, maxRequestBytes)
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
			decodedLimit := int64(MaxDecodedRequestBytes)
			if maxRequestBytes > 0 {
				decodedLimit = maxRequestBytes
			}
			req.Body = io.NopCloser(&limitedReader{reader: reader, remaining: decodedLimit})

			// The header described the bytes that arrived, not the ones the
			// handler now reads. Left in place it would be forwarded to an
			// upstream and tell a node to decompress a plain body.
			req.Header.Del(echo.HeaderContentEncoding)

			return next(c)
		}
	}
}

// limitUndecodedBody bounds a body that reaches the handler as it arrived. A
// declared length over the limit settles it without reading; a body with no
// declared length, or one that lies about it, is stopped by the reader. The
// server keeps its own reference to the original body and stays the one to
// close it, as with a decoded body above.
func limitUndecodedBody(req *http.Request, maxRequestBytes int64) {
	if maxRequestBytes <= 0 {
		return
	}
	if req.ContentLength > maxRequestBytes {
		req.Body = io.NopCloser(rejectedBody{})
		return
	}
	req.Body = io.NopCloser(&limitedReader{reader: req.Body, remaining: maxRequestBytes})
}
