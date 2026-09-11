package compression

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// ErrUnsupportedEncoding reports a Content-Encoding nodecore cannot decode.
// It is never the client's fault on the upstream edge - nodecore offers only
// the codings in Offer, so anything else is a misbehaving node.
var ErrUnsupportedEncoding = errors.New("unsupported content encoding")

// decoderMaxWindow caps the zstd window a decoder will allocate for, and with
// it the memory a single frame can make nodecore commit. The library's own cap
// is MaxWindowSize, 512MiB, and a decoder allocates the window its frame
// declares up front - so a peer that names a large one turns a few hundred
// bytes on the wire into hundreds of megabytes of heap. The peer here is
// whoever sent the body: the node upstream, and the client on the ingress,
// since both edges decode through this pool.
//
// 8MiB is the limit RFC 9659 §3 sets for this content coding: "decoders MUST
// support a Window_Size of up to and including 8 MB, and encoders MUST NOT
// generate frames requiring a Window_Size larger than 8 MB". A frame above it
// is not an HTTP zstd frame, however it was produced - and general-purpose
// zstd does produce them, `--long` alone defaults to a 128MiB window, which is
// exactly why the limit is worth enforcing rather than assuming.
const decoderMaxWindow = 8 << 20

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
// An empty body is nothing to decode, whatever coding it claims. Both codings
// have to say so together: left to themselves they disagree, gzip calling it a
// truncated header and zstd a missing magic, and a peer that labels an empty
// 204 with a coding reaches both. echo's decompress middleware went out of its
// way to let one through ("ignore if body is empty") and so did Go's
// transparent gzip upstream, so this keeps that contract on both edges.
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
	// fails here rather than halfway through the caller's first Read. A header
	// this reader rejected leaves it perfectly reusable - only the body was
	// bad - so it goes straight back to the pool either way.
	//
	// A body with nothing in it reports plain io.EOF, which the gzip reader
	// documents as a legal zero-member stream; a body that starts a header and
	// stops reports ErrUnexpectedEOF instead. That is the whole difference
	// between an empty payload and a truncated one.
	if err := reader.Reset(r); err != nil {
		gzipReaderPool.Put(reader)
		if errors.Is(err, io.EOF) {
			return io.NopCloser(r), nil
		}
		return nil, fmt.Errorf("invalid gzip body: %w", err)
	}
	return &pooledReader{
		Reader: reader,
		release: func() {
			// Close ends the flate stream. It does not drop the reader's
			// reference to r, and neither would a Reset onto a spent reader,
			// since gzip only re-points its inner flate reader once it has
			// parsed a header successfully. So a pooled gzip reader keeps the
			// finished body reachable until it is reset onto the next one -
			// bounded, since the pool itself is cleared on GC.
			_ = reader.Close()
			gzipReaderPool.Put(reader)
		},
	}, nil
}

func wrapZstdReader(r io.Reader) (io.ReadCloser, error) {
	// gzip validates its header the moment the reader is reset, so a body that
	// is not gzip is rejected before anyone reads it. zstd starts decoding
	// lazily, which would push the same mistake out to the caller's first Read
	// - as a read failure, long after the context that could explain it.
	// Checking the frame magic here restores the symmetry, and needs its first
	// four bytes buffered so the decoder still sees them.
	buffered := bufio.NewReaderSize(r, zstdPeekSize)
	empty, err := checkZstdMagic(buffered)
	if err != nil {
		return nil, err
	}
	if empty {
		return io.NopCloser(buffered), nil
	}
	return wrapZstdDecoder(buffered)
}

func wrapZstdDecoder(r *bufio.Reader) (io.ReadCloser, error) {
	pooled := zstdDecoderPool.Get()
	decoder, ok := pooled.(*zstd.Decoder)
	if !ok {
		return nil, fmt.Errorf("cannot take a zstd decoder from the pool: %w", pooled.(error))
	}
	if err := decoder.Reset(r); err != nil {
		// Reset rejects a non-nil reader for exactly one reason: the decoder
		// has been closed, which retires it for good. Dropping it instead of
		// pooling it is deliberate - unlike the gzip reader above, this one
		// would never work again.
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
// without consuming it, and separately whether there is any stream at all.
// Zero bytes is an empty payload, not a malformed frame - the same call gzip's
// reader makes on its own header.
func checkZstdMagic(r *bufio.Reader) (empty bool, err error) {
	header, err := r.Peek(zstdMagicSize)
	if errors.Is(err, io.EOF) && len(header) == 0 {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("invalid zstd body: cannot read the frame header: %w", err)
	}
	magic := binary.LittleEndian.Uint32(header)
	if magic == zstdFrameMagic {
		return false, nil
	}
	if magic >= zstdSkippableMagicMin && magic <= zstdSkippableMagicMax {
		return false, nil
	}
	return false, fmt.Errorf("invalid zstd body: frame magic %#08x is not zstd", magic)
}

// pooledReader hands its codec back to the pool when the body is done with.
//
// Close and Read run concurrently in the streaming paths: a torn-down stream
// is closed from the consuming goroutine precisely to unblock a producer
// parked in Read (see streamReadAhead in internal/server/emerald). So the
// codec is released by whichever of the two finishes last, and never while a
// Read is still inside it.
//
// Releasing under a live Read is not a theoretical problem. It would hand a
// codec that is still in use to the next request, and for zstd it deadlocks
// outright: the release path drains the decoder's output, which cannot
// complete while a read holds the decoder. Closing the underlying body - the
// caller's job, not this one's - is what lets that read return.
//
// Close may overlap a Read. Two Reads may not overlap each other: the codecs
// underneath keep decoding state that this does not serialize, and no caller
// here reads a body from two goroutines at once.
type pooledReader struct {
	io.Reader
	release func()

	mu      sync.Mutex
	readers int
	closed  bool
	freed   bool
}

func (p *pooledReader) Read(b []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, fs.ErrClosed
	}
	p.readers++
	p.mu.Unlock()

	// Deferred so a codec that panics mid-read still leaves the bookkeeping
	// straight, rather than pinning itself as permanently in use.
	defer func() {
		p.mu.Lock()
		p.readers--
		free := p.takeOwnershipLocked()
		p.mu.Unlock()
		if free {
			// Close arrived mid-read and left the codec to us.
			p.release()
		}
	}()
	return p.Reader.Read(b)
}

func (p *pooledReader) Close() error {
	p.mu.Lock()
	p.closed = true
	free := p.takeOwnershipLocked()
	p.mu.Unlock()
	if free {
		p.release()
	}
	return nil
}

// takeOwnershipLocked reports whether the caller is the one that must release
// the codec: the body is closed, no read is still inside it, and nobody has
// released it yet. Exactly one caller ever gets a true out of this.
func (p *pooledReader) takeOwnershipLocked() bool {
	if !p.closed || p.readers > 0 || p.freed {
		return false
	}
	p.freed = true
	return true
}
