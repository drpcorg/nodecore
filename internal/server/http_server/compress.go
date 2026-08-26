package http_server

// the package is adapted from echo's compress middleware
// https://github.com/labstack/echo/blob/master/middleware/compress.go
// with the hard-wired gzip codec replaced by internal/compression, which
// negotiates zstd as well, and without the MinLength buffering echo grew for
// its threshold option.

import (
	"bufio"
	"io"
	"net"
	"net/http"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

// compressResponseWriter encodes the response body with the coding the client
// negotiated. The status line is held back until the first byte of body:
// headers freeze once the status goes out, and until then we do not know
// whether this response has a body to label with a Content-Encoding.
type compressResponseWriter struct {
	http.ResponseWriter
	writer      compression.Writer
	scheme      compression.Scheme
	code        int
	wroteHeader bool
	committed   bool
}

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

			scheme := compression.Negotiate(c.Request().Header.Get(echo.HeaderAcceptEncoding))
			if scheme == compression.Identity {
				return next(c)
			}

			rw := res.Writer
			writer, err := compression.AcquireWriter(scheme, rw)
			if err != nil {
				// An unusable codec pool is an operator problem, not a reason
				// to fail the request: serving the body uncompressed is
				// something every client understands.
				log.Error().Err(err).Str("scheme", string(scheme)).Msg("couldn't acquire a compressing writer")
				return next(c)
			}

			crw := &compressResponseWriter{ResponseWriter: rw, writer: writer, scheme: scheme}
			defer func() {
				if !crw.committed {
					// Nothing was ever written, so no Content-Encoding went
					// out and the codec must not append an empty frame to the
					// body. The status still has to reach the client.
					if crw.wroteHeader {
						rw.WriteHeader(crw.code)
					}
					res.Writer = rw
					writer.Reset(io.Discard)
				}
				if closeErr := writer.Close(); closeErr != nil {
					log.Error().Err(closeErr).Msg("couldn't close a compressing writer")
				}
				compression.ReleaseWriter(writer)
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
	w.Header().Set(echo.HeaderContentEncoding, string(w.scheme)) // Issue #806
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(w.code)
	}
}

func (w *compressResponseWriter) Write(b []byte) (int, error) {
	if w.Header().Get(echo.HeaderContentType) == "" {
		w.Header().Set(echo.HeaderContentType, http.DetectContentType(b))
	}
	w.commit()
	return w.writer.Write(b)
}

// Flush pushes a streamed chunk all the way to the socket: through the codec
// first, since bytes still buffered in an encoder have not been produced yet.
func (w *compressResponseWriter) Flush() {
	w.commit()
	if err := w.writer.Flush(); err != nil {
		log.Error().Err(err).Msg("couldn't flush a compressing writer")
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
