package compression

import (
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// encoderWindow caps the back-reference distance of a pooled zstd encoder,
// and with it the memory each one holds while idle in the pool. JSON-RPC and
// REST bodies repeat within a few kilobytes - method names, hex prefixes, key
// names - so the default multi-megabyte window would buy almost no ratio for
// memory multiplied by every encoder in flight.
const encoderWindow = 256 << 10

var gzipWriterPool = sync.Pool{
	New: func() any {
		// BestSpeed is what the ingress has always used: on a proxy the
		// compression sits on the request's critical path, so CPU time is
		// worth more than the last few percent of ratio.
		writer, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		if err != nil {
			return err
		}
		return writer
	},
}

var zstdEncoderPool = sync.Pool{
	New: func() any {
		encoder, err := zstd.NewWriter(
			io.Discard,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			// Concurrency 1 keeps an encoder to one synchronous worker. The
			// default spawns GOMAXPROCS goroutines per encoder, which on a
			// proxy holding thousands of concurrent responses is a goroutine
			// count nobody asked for.
			zstd.WithEncoderConcurrency(1),
			zstd.WithWindowSize(encoderWindow),
		)
		if err != nil {
			return err
		}
		return encoder
	},
}

// Writer is a compressing writer for one response body. Both pooled codecs
// satisfy it natively.
type Writer interface {
	io.WriteCloser
	// Flush pushes everything written so far to the underlying writer, so a
	// streamed chunk reaches the client without waiting for Close.
	Flush() error
	// Reset redirects the writer, discarding any state from a previous body.
	Reset(w io.Writer)
}

// AcquireWriter takes a pooled encoder for scheme, encoding into w. The
// caller must Close it to terminate the stream and then ReleaseWriter it.
// Identity is not an encoder and is rejected.
func AcquireWriter(scheme Scheme, w io.Writer) (Writer, error) {
	var pooled any
	switch scheme {
	case Gzip:
		pooled = gzipWriterPool.Get()
	case Zstd:
		pooled = zstdEncoderPool.Get()
	default:
		return nil, fmt.Errorf("%w: no encoder for %q", ErrUnsupportedEncoding, scheme)
	}
	writer, ok := pooled.(Writer)
	if !ok {
		return nil, fmt.Errorf("cannot take a %s writer from the pool: %w", scheme, pooled.(error))
	}
	writer.Reset(w)
	return writer, nil
}

// ReleaseWriter returns a writer to its pool. It resets the writer onto
// io.Discard first so a pooled encoder never pins the response it just
// finished writing to.
func ReleaseWriter(w Writer) {
	w.Reset(io.Discard)
	switch writer := w.(type) {
	case *gzip.Writer:
		gzipWriterPool.Put(writer)
	case *zstd.Encoder:
		zstdEncoderPool.Put(writer)
	}
}
