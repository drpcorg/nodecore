package compression

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	brrr "github.com/molecule-man/go-brrr"
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

// brotliPeekSize is the smallest buffer bufio allows. The peek only needs one
// byte, and past the buffer the decoder reads straight from the body.
const brotliPeekSize = 16

// errBrotliTrailingData ends the read of a body that carries bytes after the
// end of its brotli stream.
var errBrotliTrailingData = errors.New("data after the end of the brotli stream")

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

// Brotli readers are pooled for the state a decode builds - its ring buffer,
// output buffer and Huffman tables - which go-brrr keeps across Reset, so a
// warm reader decodes the next body without allocating. That is also what a
// parked reader holds, so only a reader whose stream declared at most
// brotliParkedMaxWindowBits goes back to the pool; one from a larger window is
// dropped, ring buffer and all.
//
// The window is not capped below RFC 7932's own limit, lgwin 24 (16MiB). That
// is what the reference encoder declares whenever it compresses a pipe, so a
// lower cap would refuse conformant bodies from clients and fail conformant
// nodes. The non-standard large-window form, which goes up to 1GiB, is refused
// by the decoder itself. The price is on crafted input: a stream can make one
// decode grow its ring buffer to the declared window and flush a ring buffer's
// worth of output, about 32MiB at lgwin 24 - bounded per request, as zstd's
// window cap bounds a zstd frame.
var brotliReaderPool readerPool = &sync.Pool{
	New: func() any { return brrr.NewReader(nil) },
}

// readerPool is the part of sync.Pool the brotli reader pool is used through,
// so a test can count what goes in and out of it.
type readerPool interface {
	Get() any
	Put(x any)
}

// brotliParkedMaxWindowBits is the largest window, lgwin 22 (4MiB), that a
// pooled brotli reader is kept for. It covers what encoders declare by default
// - the reference library's lgwin 22, nodecore's own lgwin 18.
//
// What a parked reader holds then grows with the bodies it has decoded: its
// ring buffer and output buffer are sized to the output, so after ordinary RPC
// bodies each is at most the largest body rounded up to a power of two. The
// worst case, on crafted input, is about 16MiB: an output buffer of the 4MiB
// window plus append's slack, about 5MiB; a ring buffer of up to 8MiB, since
// go-brrr recycles the smaller rings any decode grows out of and a warm reader
// may be handed one outgrown by a larger window; and up to about 2.5MiB of
// Huffman tables for a stream that uses the maximum number of them. A pooled
// zstd decoder may keep 8MiB.
const brotliParkedMaxWindowBits = 22

// WrapReader returns a reader that decodes r according to contentEncoding.
// An empty or identity encoding passes r through untouched.
//
// The returned Close releases the pooled codec and MUST be called; it does
// not close r, whose lifetime stays with the caller.
// An empty body is nothing to decode, whatever coding it claims. Every coding
// has to say so: left to themselves they disagree, gzip calling it a truncated
// header, zstd a missing magic and brotli a truncated stream, and a peer that
// labels an empty 204 with a coding reaches all three. echo's decompress
// middleware went out of its way to let one through ("ignore if body is
// empty") and so did Go's transparent gzip upstream, so this keeps that
// contract on both edges.
func WrapReader(contentEncoding string, r io.Reader) (io.ReadCloser, error) {
	switch Scheme(strings.ToLower(strings.TrimSpace(contentEncoding))) {
	case Identity, "identity":
		return io.NopCloser(r), nil
	case Gzip:
		return wrapGzipReader(r)
	case Zstd:
		return wrapZstdReader(r)
	case Brotli:
		return wrapBrotliReader(r)
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

func wrapBrotliReader(r io.Reader) (io.ReadCloser, error) {
	// go-brrr reports a body with no bytes at all as a truncated stream, which
	// would fail the empty payload gzip and zstd both let through. One byte of
	// lookahead tells the two apart without consuming anything.
	buffered := bufio.NewReaderSize(r, brotliPeekSize)
	header, err := buffered.Peek(1)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return io.NopCloser(buffered), nil
		}
		return nil, fmt.Errorf("invalid brotli body: cannot read the stream header: %w", err)
	}
	lgwin := brotliStreamWindowBits(header[0])

	decoder := acquireBrotliReader(lgwin)
	decoder.Reset(buffered)
	stream := &brotliStream{decoder: decoder, source: buffered}

	// brotli has no magic number, so the check zstd gets from its frame magic
	// is done here by decoding: one byte of output means the window bits and
	// the first meta-block header parsed. Plaintext, gzip and zstd bodies
	// under a br label fail here, and so does the large-window form. It is
	// best effort, as gzip's header check is - a body whose leading bits
	// happen to parse fails on a later read instead - and it blocks until the
	// first meta-block yields output, the way zstd's peek blocks on its magic.
	var first [1]byte
	n, err := stream.Read(first[:])
	if err != nil && !errors.Is(err, io.EOF) {
		releaseBrotliReader(decoder, lgwin)
		return nil, fmt.Errorf("invalid brotli body: %w", err)
	}
	if n == 0 && errors.Is(err, io.EOF) {
		// A complete stream that decodes to nothing: the one-byte empty
		// stream. There is nothing left for a decoder to do.
		releaseBrotliReader(decoder, lgwin)
		return io.NopCloser(buffered), nil
	}
	return &pooledReader{
		// The byte decoded above goes back in front of the rest.
		Reader:  io.MultiReader(bytes.NewReader(first[:n]), stream),
		release: func() { releaseBrotliReader(decoder, lgwin) },
	}, nil
}

// acquireBrotliReader takes a warm decoder from the pool for a stream it may be
// parked from afterwards. A stream with a larger window gets a fresh one, which
// is dropped when the body is done, so it never costs the pool a warm reader.
// That includes a few first bytes that are not large windows in practice -
// the reference encoder's empty stream 0x3f, and gzip's 0x1f or a BOM
// mislabelled br - each costing one reader's allocation.
func acquireBrotliReader(lgwin int) *brrr.Reader {
	if !brotliWindowParks(lgwin) {
		return brrr.NewReader(nil)
	}
	// New cannot fail, so the pool holds nothing but readers.
	return brotliReaderPool.Get().(*brrr.Reader)
}

// releaseBrotliReader returns a decoder to the pool warm, or drops it if its
// stream declared a window too large to park.
func releaseBrotliReader(decoder *brrr.Reader, lgwin int) {
	if parkBrotliReader(decoder, lgwin) {
		brotliReaderPool.Put(decoder)
	}
}

// parkBrotliReader lets go of the body a decoder was reading and reports
// whether the decoder is fit to pool. Reset keeps its decode state warm for the
// next body. A decoder from a larger window - or from the large-window form,
// which it refuses anyway - is left alone, not Closed: Close would hand its
// ring buffer to go-brrr's own pool, and the next decoder to grow its ring
// would take it from there and park it warm, whatever its own window. Left
// alone, the ring goes to the GC with the decoder.
func parkBrotliReader(decoder *brrr.Reader, lgwin int) bool {
	if !brotliWindowParks(lgwin) {
		return false
	}
	decoder.Reset(nil)
	return true
}

// brotliWindowParks reports whether a decoder may be pooled after a stream
// that declared lgwin; 0 is the large-window form.
func brotliWindowParks(lgwin int) bool {
	return lgwin != 0 && lgwin <= brotliParkedMaxWindowBits
}

// brotliStreamWindowBits decodes the window a brotli stream declares in its
// first byte (RFC 7932 §9.1, read least significant bit first), or 0 for the
// pattern the format reserves - which is how the large-window form opens.
func brotliStreamWindowBits(first byte) int {
	if first&1 == 0 {
		return 16
	}
	if n := (first >> 1) & 7; n != 0 {
		return 17 + int(n)
	}
	switch m := (first >> 4) & 7; m {
	case 0:
		return 17
	case 1:
		return 0
	default:
		return 8 + int(m)
	}
}

// brotliStream reads one brotli stream and checks that the body ends with it.
// brotli has no concatenation - a second stream is not a continuation, the way
// another gzip member or zstd frame is - and bytes after the end are rejected,
// as gzip and zstd reject them. go-brrr notices them itself only when they
// share a buffer with the end of the stream; reading one byte past it makes
// the answer independent of how the body arrived.
type brotliStream struct {
	decoder *brrr.Reader
	source  io.Reader
	err     error
}

func (s *brotliStream) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.decoder.Read(p)
	if errors.Is(err, io.EOF) {
		var probe [1]byte
		switch extra, probeErr := io.ReadFull(s.source, probe[:]); {
		case extra > 0:
			err = errBrotliTrailingData
		case !errors.Is(probeErr, io.EOF):
			// The stream is complete but the body failed after it. gzip and
			// zstd read on past their last member or frame too, and would
			// report the same failure.
			err = probeErr
		}
	}
	s.err = err
	return n, err
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
		release := p.takeOwnershipLocked()
		p.mu.Unlock()
		if release != nil {
			// Close arrived mid-read and left the codec to us.
			release()
		}
	}()
	return p.Reader.Read(b)
}

func (p *pooledReader) Close() error {
	p.mu.Lock()
	p.closed = true
	release := p.takeOwnershipLocked()
	p.mu.Unlock()
	if release != nil {
		release()
	}
	return nil
}

// takeOwnershipLocked hands the caller the release func if it is the one that
// must release the codec: the body is closed, no read is still inside it, and
// nobody has released it yet. Exactly one caller ever gets it. The body forgets
// the codec as it does - no Read can reach it again - because a closed body
// can stay reachable long after: the ingress leaves it on the request, which
// echo keeps in a pooled context. A codec dropped rather than pooled, like a
// brotli decoder from a large window, would otherwise stay reachable with it.
func (p *pooledReader) takeOwnershipLocked() func() {
	if !p.closed || p.readers > 0 || p.freed {
		return nil
	}
	p.freed = true
	release := p.release
	p.Reader, p.release = nil, nil
	return release
}
