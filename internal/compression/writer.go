package compression

import (
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"
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

// brotliQuality and brotliWindowBits configure the pooled brotli encoder,
// chosen as the other two were: cheapest first. Measured on mainnet bodies
// against the gzip and zstd encoders above, pooled (µs -> bytes out):
//
//	payload                  gzip BestSpeed     zstd SpeedFastest   br q1, lgwin 18
//	45 B eth_blockNumber     1.4 -> 70          0.1 -> 58           2.0 -> 49
//	26 KB block, hashes      102 -> 12,921      104 -> 12,324       99 -> 13,690
//	497 KB eth_getLogs       625 -> 50,330      589 -> 40,730       348 -> 44,356
//	668 KB block, full txs   1,359 -> 112,505   1,224 -> 101,030    719 -> 105,523
//
// Quality 1 costs about half of gzip's CPU on large bodies and comes out
// smaller. Quality 0 is cheaper again but larger than gzip there, which is the
// one thing brotli is offered for; quality 2 cost 1.2-2.4x quality 1's CPU for
// under 1% on large bodies. The window is zstd's encoderWindow for the same
// reason as zstd's: lgwin 16 came out both larger and slower on the large
// bodies, and lgwin 22 bought 1-2% for another 1.3MiB held by every pooled
// encoder. At quality 1 the encoder never announces less than lgwin 18.
const (
	brotliQuality    = 1
	brotliWindowBits = 18 // 256KiB, encoderWindow's size
)

// A pooled brotli writer is Reset on every checkout, and that is also what
// keeps its compressor warm: go-brrr frees the compressor on Close only for a
// writer that has never been Reset.
var brotliWriterPool = sync.Pool{
	New: func() any {
		writer, err := brrr.NewWriterOptions(io.Discard, brotliQuality,
			brrr.WriterOptions{LGWin: brotliWindowBits})
		if err != nil {
			return err
		}
		return writer
	},
}

// Writer is a compressing writer for one response body. Every pooled codec
// satisfies it natively.
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
	case Brotli:
		pooled = brotliWriterPool.Get()
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
	case *brrr.Writer:
		brotliWriterPool.Put(writer)
	}
}
