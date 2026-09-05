package compression

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// ErrUnsupportedEncoding reports a Content-Encoding nodecore cannot decode.
// It is never the client's fault on the upstream edge - nodecore offers only
// the codings in Offer, so anything else is a misbehaving node.
var ErrUnsupportedEncoding = errors.New("unsupported content encoding")

// decoderMaxWindow caps the zstd window a decoder will allocate for. The
// library defaults to 64GiB, which lets a hostile or broken peer name a
// window far larger than any real HTTP body needs and make nodecore allocate
// it. Real encoders top out at 8MiB at these levels, so 64MiB accepts
// everything legitimate with room to spare.
const decoderMaxWindow = 64 << 20

// Frame magic numbers from RFC 8878 §3.1: one for a regular frame, and a
// range for skippable frames, which a stream is allowed to lead with.
const (
	zstdMagicSize         = 4
	zstdPeekSize          = 512
	zstdFrameMagic        = 0xFD2FB528
	zstdSkippableMagicMin = 0x184D2A50
	zstdSkippableMagicMax = 0x184D2A5F
)

var gzipReaderPool = sync.Pool{
	New: func() any { return new(gzip.Reader) },
}

// Decoders are pooled rather than created per response: a zstd decoder
// allocates its window up front, which is far too expensive to repeat on
// every proxied request. Concurrency 1 keeps a pooled decoder to a single
// synchronous worker instead of one goroutine per GOMAXPROCS per decoder.
var zstdDecoderPool = sync.Pool{
	New: func() any {
		decoder, err := zstd.NewReader(
			nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(decoderMaxWindow),
		)
		if err != nil {
			return err
		}
		return decoder
	},
}

// WrapReader returns a reader that decodes r according to contentEncoding.
// An empty or identity encoding passes r through untouched.
//
// The returned Close releases the pooled codec and MUST be called; it does
// not close r, whose lifetime stays with the caller.
func WrapReader(contentEncoding string, r io.Reader) (io.ReadCloser, error) {
	switch Scheme(strings.ToLower(strings.TrimSpace(contentEncoding))) {
	case Identity, "identity":
		return io.NopCloser(r), nil
	case Gzip:
		return wrapGzipReader(r)
	case Zstd:
		return wrapZstdReader(r)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, contentEncoding)
	}
}

func wrapGzipReader(r io.Reader) (io.ReadCloser, error) {
	pooled := gzipReaderPool.Get()
	reader, ok := pooled.(*gzip.Reader)
	if !ok {
		return nil, fmt.Errorf("cannot take a gzip reader from the pool: %w", pooled.(error))
	}
	// Reset parses the gzip header eagerly, so a body that is not gzip at all
	// fails here rather than halfway through the caller's first Read.
	if err := reader.Reset(r); err != nil {
		gzipReaderPool.Put(reader)
		return nil, fmt.Errorf("invalid gzip body: %w", err)
	}
	return &pooledReader{
		Reader: reader,
		release: func() {
			// Close drops the reference to r without touching r itself, so a
			// pooled reader never pins a finished response body.
			_ = reader.Close()
			gzipReaderPool.Put(reader)
		},
	}, nil
}

func wrapZstdReader(r io.Reader) (io.ReadCloser, error) {
	// gzip validates its header the moment the reader is reset, so a body
	// that is not gzip is rejected before anyone reads it. zstd starts
	// decoding lazily, which would push the same mistake out to the caller's
	// first Read - as a read failure, long after the context that could
	// explain it. Checking the frame magic here restores the symmetry.
	buffered := bufio.NewReaderSize(r, zstdPeekSize)
	if err := checkZstdMagic(buffered); err != nil {
		return nil, err
	}

	pooled := zstdDecoderPool.Get()
	decoder, ok := pooled.(*zstd.Decoder)
	if !ok {
		return nil, fmt.Errorf("cannot take a zstd decoder from the pool: %w", pooled.(error))
	}
	if err := decoder.Reset(buffered); err != nil {
		return nil, fmt.Errorf("invalid zstd body: %w", err)
	}
	return &pooledReader{
		Reader: decoder,
		release: func() {
			// Reset(nil) drains any undelivered output and drops r. Close is
			// deliberately not called: it retires the decoder permanently,
			// which would defeat the pool.
			_ = decoder.Reset(nil)
			zstdDecoderPool.Put(decoder)
		},
	}, nil
}

// checkZstdMagic reports whether the stream opens with a zstd frame header,
// without consuming it. An empty body is not a malformed frame: a zero-byte
// payload decodes to zero bytes.
func checkZstdMagic(r *bufio.Reader) error {
	header, err := r.Peek(zstdMagicSize)
	if errors.Is(err, io.EOF) && len(header) == 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("invalid zstd body: cannot read the frame header: %w", err)
	}
	magic := binary.LittleEndian.Uint32(header)
	if magic == zstdFrameMagic {
		return nil
	}
	if magic >= zstdSkippableMagicMin && magic <= zstdSkippableMagicMax {
		return nil
	}
	return fmt.Errorf("invalid zstd body: frame magic %#08x is not zstd", magic)
}

// pooledReader hands a decoder back to its pool on Close. Close is idempotent
// because a streaming response can be torn down from both the read side and
// an explicit teardown, and returning one decoder to the pool twice would let
// two requests decode through the same one.
type pooledReader struct {
	io.Reader
	release func()
	once    sync.Once
}

func (p *pooledReader) Close() error {
	p.once.Do(p.release)
	return nil
}
