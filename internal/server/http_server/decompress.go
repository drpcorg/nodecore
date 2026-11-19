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
	// exceeded is set once the body is known to run past the limit. Whoever
	// reads through a decoder asks here rather than the decoder, which may
	// report the failed read as an error of its own.
	exceeded bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.exceeded {
		return 0, errRequestBodyTooLarge
	}
	if l.remaining <= 0 {
		// The cap has been handed over in full. One more byte settles whether
		// the body ended exactly on it or runs past it.
		n, err := l.reader.Read(l.probe[:])
		if n > 0 {
			l.exceeded = true
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

// decodedBody is a decoded body whose compressed form is bounded as well. The
// decoder sits between the two limits and reads the wire on its own schedule,
// so when the wire limit is hit the decoder is the one that sees it - and what
// it then reports is up to the decoder: the limit error, an error of its own
// about a truncated frame, or a clean end. The body is too large in every one
// of those cases, and a read that did not succeed says so.
type decodedBody struct {
	reader io.Reader
	wire   *limitedReader
}

func (d *decodedBody) Read(p []byte) (int, error) {
	n, err := d.reader.Read(p)
	if err != nil && d.wire != nil && d.wire.exceeded {
		return n, errRequestBodyTooLarge
	}
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
// MaxDecodedRequestBytes - bytes on the wire are bytes the client had to send
// in full, which is its own limit. A positive value bounds every body as it
// arrives, and a decoded body a second time on what it decodes to, in place of
// MaxDecodedRequestBytes. Both bounds are needed: a decoded size says little
// about the wire, since a coding may carry any amount of framing that decodes
// to nothing - empty gzip members, skippable zstd frames.
func Decompress(maxRequestBytes int64) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			wire, declaredTooLarge := limitBodyOnTheWire(req, maxRequestBytes)

			encoding := strings.TrimSpace(req.Header.Get(echo.HeaderContentEncoding))
			if encoding == "" || strings.EqualFold(encoding, "identity") {
				return next(c)
			}

			reader, err := compression.WrapReader(encoding, req.Body)
			if errors.Is(err, compression.ErrUnsupportedEncoding) {
				return next(c)
			}
			if err != nil && (declaredTooLarge || (wire != nil && wire.exceeded)) {
				// The decoders read the head of a body to check it, and for
				// this body that read already ran into the limit. It is the
				// handler's read that has to fail: only the handler knows the
				// shape its protocol reports an oversized body in.
				req.Body = io.NopCloser(rejectedBody{})
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
			req.Body = io.NopCloser(&decodedBody{
				reader: &limitedReader{reader: reader, remaining: decodedLimit},
				wire:   wire,
			})

			// The header described the bytes that arrived, not the ones the
			// handler now reads. Left in place it would be forwarded to an
			// upstream and tell a node to decompress a plain body.
			req.Header.Del(echo.HeaderContentEncoding)

			return next(c)
		}
	}
}

// limitBodyOnTheWire bounds a body as it arrives, before any decoding. A
// declared length over the limit settles it without reading, and is reported
// as declaredTooLarge; a body with no declared length, or one that lies about
// it, is stopped by the returned reader, which is nil when nothing is limited
// or nothing is left to read. The server keeps its own reference to the
// original body and stays the one to close it, as with a decoded body above.
func limitBodyOnTheWire(req *http.Request, maxRequestBytes int64) (wire *limitedReader, declaredTooLarge bool) {
	if maxRequestBytes <= 0 {
		return nil, false
	}
	if req.ContentLength > maxRequestBytes {
		req.Body = io.NopCloser(rejectedBody{})
		return nil, true
	}
	wire = &limitedReader{reader: req.Body, remaining: maxRequestBytes}
	req.Body = io.NopCloser(wire)
	return wire, false
}
