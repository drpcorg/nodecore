package http_server

// the package is adapted from echo's compress middleware
// https://github.com/labstack/echo/blob/master/middleware/compress.go
// with the hard-wired gzip codec replaced by internal/compression, which
// negotiates zstd as well, and without the MinLength buffering echo grew for
// its threshold option.

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

// compressResponseWriter encodes the response body with the coding the client
// negotiated. The status line is held back until the first byte of body:
// headers freeze once the status goes out, and until then we do not know
// whether this response has a body to label with a Content-Encoding.
//
// The encoder is taken at that same moment, so writer stays nil for as long as
// the response is uncommitted - and for good on a response that never has a
// body to encode.
type compressResponseWriter struct {
	http.ResponseWriter
	writer      compression.Writer
	scheme      compression.Scheme
	code        int
	wroteHeader bool
	committed   bool
	released    bool
}

// errResponseFinished reports a write that arrived after the body was closed
// off. There is nowhere left to put those bytes: the stream on the wire is
// already terminated, and the codec that produced it has gone back to the
// pool, where it may already belong to another response.
var errResponseFinished = errors.New("the response body is already finished")

// Compress returns a middleware that compresses the response body with the
// coding the client asked for - zstd or gzip, whichever Negotiate picks.
// A client that asks for neither is served plain bytes.
func Compress() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			res := c.Response()
			// Announced even when nothing is compressed: a cache that skips
			// this key hands a zstd body to a gzip-only client.
			res.Header().Add(echo.HeaderVary, echo.HeaderAcceptEncoding)

			// Joined, not Get: a client is allowed to send Accept-Encoding as
			// several field lines, and RFC 9110 §5.3 says they mean the same
			// as one comma-separated line. Get would see only the first, so
			// "Accept-Encoding: zstd;q=0" followed by "Accept-Encoding: gzip"
			// would lose the refusal.
			scheme := compression.Negotiate(
				strings.Join(c.Request().Header.Values(echo.HeaderAcceptEncoding), ","),
			)
			if scheme == compression.Identity {
				return next(c)
			}

			rw := res.Writer
			crw := &compressResponseWriter{ResponseWriter: rw, scheme: scheme}
			defer func() {
				if !crw.committed {
					// Nothing was ever written, so no encoder was ever taken
					// and no Content-Encoding went out. The status still has
					// to reach the client.
					if crw.wroteHeader {
						rw.WriteHeader(crw.code)
					}
					res.Writer = rw
				}
				if crw.writer != nil {
					if closeErr := crw.writer.Close(); closeErr != nil {
						log.Error().Err(closeErr).Msg("couldn't close a compressing writer")
					}
					compression.ReleaseWriter(crw.writer)
				}
				// From here the codec belongs to whoever takes it out of the
				// pool next. Anything still holding this writer - echo's
				// error handler, an outer middleware - has to be turned away
				// rather than allowed to write through it.
				crw.released = true
			}()
			res.Writer = crw

			return next(c)
		}
	}
}

func (w *compressResponseWriter) WriteHeader(code int) {
	w.Header().Del(echo.HeaderContentLength) // Issue #444
	w.wroteHeader = true
	w.code = code
}

// commit labels the response with its coding and releases the held status.
// It runs exactly once, on whichever comes first of the first body byte and
// an explicit flush.
func (w *compressResponseWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	// The encoder is taken here rather than when the middleware wrapped the
	// response, because "this response has a body" is the only thing that
	// makes one worth holding. A handler that writes none and returns at once
	// would merely waste a pool round-trip; one that writes none and then
	// stays - a WebSocket upgrade hijacks the connection and lives as long as
	// the socket does - would pin a megabyte of encoder it never writes a
	// single byte through, once per connection, for hours.
	writer, err := compression.AcquireWriter(w.scheme, w.ResponseWriter)
	if err != nil {
		// An unusable codec pool is an operator problem, not a reason to fail
		// the request: serving the body uncompressed is something every client
		// understands, and leaving w.writer nil is what does that.
		log.Error().Err(err).Str("scheme", string(w.scheme)).Msg("couldn't acquire a compressing writer")
	} else {
		w.writer = writer
		w.Header().Set(echo.HeaderContentEncoding, string(w.scheme)) // Issue #806
	}
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(w.code)
	}
}

func (w *compressResponseWriter) Write(b []byte) (int, error) {
	if w.released {
		return 0, errResponseFinished
	}
	if w.Header().Get(echo.HeaderContentType) == "" {
		w.Header().Set(echo.HeaderContentType, http.DetectContentType(b))
	}
	w.commit()
	if w.writer == nil {
		return w.ResponseWriter.Write(b)
	}
	return w.writer.Write(b)
}

// Flush pushes a streamed chunk all the way to the socket: through the codec
// first, since bytes still buffered in an encoder have not been produced yet.
func (w *compressResponseWriter) Flush() {
	if w.released {
		return
	}
	w.commit()
	if w.writer != nil {
		if err := w.writer.Flush(); err != nil {
			log.Error().Err(err).Msg("couldn't flush a compressing writer")
		}
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *compressResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

func (w *compressResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *compressResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
