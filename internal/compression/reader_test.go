package compression_test

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gzipBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func zstdBytes(t *testing.T, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	_, err = w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestWrapReaderDecodesSupportedCodings(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`)
	tests := []struct {
		name            string
		contentEncoding string
		body            []byte
	}{
		{"gzip", "gzip", gzipBytes(t, plain)},
		{"zstd", "zstd", zstdBytes(t, plain)},
		{"case-insensitive", "ZSTD", zstdBytes(t, plain)},
		{"no encoding is passed through", "", plain},
		{"identity is passed through", "identity", plain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			reader, err := compression.WrapReader(tt.contentEncoding, bytes.NewReader(tt.body))
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()

			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

// An upstream answering a coding nodecore never offered must be reported, not
// passed on: the body would reach the client as bytes it cannot read, and the
// connector strips Content-Encoding so it would not even know why.
func TestWrapReaderRejectsUnsupportedCodings(t *testing.T) {
	for _, contentEncoding := range []string{"br", "deflate", "gzip, gzip"} {
		t.Run(contentEncoding, func(te *testing.T) {
			_, err := compression.WrapReader(contentEncoding, bytes.NewReader(nil))

			assert.ErrorIs(te, err, compression.ErrUnsupportedEncoding)
		})
	}
}

// Codecs are pooled, so a reader returned by Close must come back clean:
// a decoder still holding the previous stream's state produces garbage on
// its next use.
func TestWrapReaderIsReusableAfterClose(t *testing.T) {
	for _, scheme := range []string{"gzip", "zstd"} {
		t.Run(scheme, func(te *testing.T) {
			for _, plain := range [][]byte{[]byte("first body"), []byte("a completely different second body")} {
				var body []byte
				if scheme == "gzip" {
					body = gzipBytes(te, plain)
				} else {
					body = zstdBytes(te, plain)
				}

				reader, err := compression.WrapReader(scheme, bytes.NewReader(body))
				require.NoError(te, err)
				got, err := io.ReadAll(reader)
				require.NoError(te, err)
				require.NoError(te, reader.Close())

				assert.Equal(te, plain, got)
			}
		})
	}
}

// A truncated frame is a broken upstream, not a panic: the error must reach
// the caller so the request fails cleanly.
func TestWrapReaderReportsCorruptBody(t *testing.T) {
	truncated := zstdBytes(t, bytes.Repeat([]byte("x"), 1024))[:20]

	reader, err := compression.WrapReader("zstd", bytes.NewReader(truncated))
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	_, err = io.ReadAll(reader)

	assert.Error(t, err)
}

// A frame that does not start with zstd's magic number is not zstd at all,
// and saying so at wrap time turns a peer's mislabelled body into a clean
// rejection instead of an error surfacing mid-read from somewhere deeper.
func TestWrapReaderRejectsBodyThatIsNotZstd(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"plain json", []byte(`{"jsonrpc":"2.0","id":1}`)},
		{"gzip bytes under a zstd label", gzipBytes(t, []byte("hello"))},
		{"magic truncated", zstdBytes(t, []byte("hello"))[:2]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(te *testing.T) {
			_, err := compression.WrapReader("zstd", bytes.NewReader(tt.body))

			assert.Error(te, err)
			assert.NotErrorIs(te, err, compression.ErrUnsupportedEncoding,
				"the coding is supported; it is this body that is wrong")
		})
	}
}

// An empty body is a legitimate zero-byte payload, not a malformed frame -
// and every coding has to say so. Left to the codecs they disagree: gzip
// calls it a truncated header and zstd a missing magic. A peer that labels an
// empty 204 with a coding used to be served by both echo's decompress
// middleware and Go's transparent gzip, and it stays served here.
func TestWrapReaderAcceptsAnEmptyBodyUnderEveryCoding(t *testing.T) {
	for _, contentEncoding := range []string{"zstd", "gzip", "identity", ""} {
		name := contentEncoding
		if name == "" {
			name = "absent"
		}
		t.Run(name, func(te *testing.T) {
			reader, err := compression.WrapReader(contentEncoding, bytes.NewReader(nil))
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()

			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Empty(te, got)
		})
	}
}

// A zstd frame declares its own window and the decoder allocates that much up
// front, so the cap is the only thing standing between a few hundred bytes on
// the wire and tens of megabytes of heap - on the ingress, where the bytes
// come from a client, as much as upstream.
func TestWrapReaderRejectsFramesAboveTheWindowCap(t *testing.T) {
	// Well above the 8MiB cap, and far above anything a real encoder emits.
	oversized := zstdBytesWithWindow(t, bytes.Repeat([]byte("nodecore"), 1<<20), 64<<20)

	reader, err := compression.WrapReader("zstd", bytes.NewReader(oversized))
	if err == nil {
		defer func() { _ = reader.Close() }()
		_, err = io.ReadAll(reader)
	}

	assert.Error(t, err, "a frame demanding more window than the cap must be refused, not allocated")
}

// The cap has to clear every window a real encoder asks for, or legitimate
// bodies start failing. `zstd -19` and klauspost's best level both declare
// exactly 8MiB; nothing mainstream goes higher.
func TestWrapReaderAcceptsEveryWindowRealEncodersEmit(t *testing.T) {
	plain := bytes.Repeat([]byte(`{"jsonrpc":"2.0","method":"eth_call"},`), 1<<15)

	for _, window := range []int{1 << 10, 128 << 10, 1 << 20, 4 << 20, 8 << 20} {
		t.Run(fmt.Sprintf("window=%dKiB", window>>10), func(te *testing.T) {
			reader, err := compression.WrapReader("zstd",
				bytes.NewReader(zstdBytesWithWindow(te, plain, window)))
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()

			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

func zstdBytesWithWindow(t *testing.T, plain []byte, window int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf, zstd.WithWindowSize(window), zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	_, err = w.Write(plain)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// parkingReader hands out the first prefixLen bytes of a body and then parks,
// the way a read on a live connection parks waiting for the next packet. It
// says so on `parked`, so a test can close the reader at a moment when a Read
// is provably inside it rather than probably inside it.
type parkingReader struct {
	body      []byte
	i         int
	prefixLen int
	parked    chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (r *parkingReader) Read(p []byte) (int, error) {
	if r.i < r.prefixLen {
		n := copy(p, r.body[r.i:min(r.prefixLen, len(r.body))])
		r.i += n
		return n, nil
	}
	r.once.Do(func() { close(r.parked) })
	<-r.release
	return 0, io.ErrUnexpectedEOF
}

// A streamed body is closed from the consuming goroutine while the producing
// one is still inside Read - that is how internal/server/emerald unblocks a
// parked producer when a stream is torn down early. The codec must not be
// handed back to the pool underneath that read: the next request would decode
// through a codec still in use, and for zstd the release path deadlocks
// against the read outright.
func TestWrapReaderSurvivesACloseDuringARead(t *testing.T) {
	for _, contentEncoding := range []string{"zstd", "gzip"} {
		t.Run(contentEncoding, func(te *testing.T) {
			plain := bytes.Repeat([]byte("payload-payload-"), 4096)
			frame := zstdBytes(te, plain)
			if contentEncoding == "gzip" {
				frame = gzipBytes(te, plain)
			}

			for range 10 {
				source := &parkingReader{
					body:      frame,
					prefixLen: len(frame) / 2,
					parked:    make(chan struct{}),
					release:   make(chan struct{}),
				}
				reader, err := compression.WrapReader(contentEncoding, source)
				require.NoError(te, err)

				readDone := make(chan struct{})
				go func() {
					defer close(readDone)
					_, _ = io.Copy(io.Discard, reader)
				}()

				// A Read is now provably inside the source, not probably.
				<-source.parked

				closed := make(chan struct{})
				go func() {
					defer close(closed)
					require.NoError(te, reader.Close())
				}()
				select {
				case <-closed:
				case <-time.After(10 * time.Second):
					te.Fatal("Close blocked behind a read it was supposed to outlive")
				}

				// Only now let the read finish, as closing the body would.
				close(source.release)
				select {
				case <-readDone:
				case <-time.After(10 * time.Second):
					te.Fatal("the parked read never returned")
				}

				// Whatever the pool holds now must belong to nobody else.
				other, err := compression.WrapReader(contentEncoding, bytes.NewReader(frame))
				require.NoError(te, err)
				got, err := io.ReadAll(other)
				require.NoError(te, err)
				require.NoError(te, other.Close())
				require.Equal(te, plain, got, "a codec was released while it was still being read")
			}
		})
	}
}

// Reading a body after it has been closed must be refused rather than reach a
// codec that now belongs to someone else.
func TestWrapReaderRefusesReadsAfterClose(t *testing.T) {
	for _, contentEncoding := range []string{"zstd", "gzip"} {
		t.Run(contentEncoding, func(te *testing.T) {
			frame := zstdBytes(te, []byte("payload"))
			if contentEncoding == "gzip" {
				frame = gzipBytes(te, []byte("payload"))
			}
			reader, err := compression.WrapReader(contentEncoding, bytes.NewReader(frame))
			require.NoError(te, err)
			require.NoError(te, reader.Close())
			require.NoError(te, reader.Close(), "Close stays idempotent")

			_, err = reader.Read(make([]byte, 8))

			assert.ErrorIs(te, err, fs.ErrClosed)
		})
	}
}

// dribbleReader hands out one byte per Read with a short pause, so a teardown
// racing it lands at an unpredictable point inside the codec rather than at
// the one boundary a barrier can arrange. The barrier test above pins the
// contract; this one goes looking for the interleavings.
type dribbleReader struct {
	body []byte
	i    int
	stop chan struct{}
}

func (r *dribbleReader) Read(p []byte) (int, error) {
	select {
	case <-r.stop:
		return 0, io.ErrUnexpectedEOF
	case <-time.After(50 * time.Microsecond):
	}
	if r.i >= len(r.body) {
		return 0, io.EOF
	}
	n := copy(p[:1], r.body[r.i:])
	r.i += n
	return n, nil
}

func TestWrapReaderSurvivesATornDownStreamUnderRacingTeardown(t *testing.T) {
	for _, contentEncoding := range []string{"zstd", "gzip"} {
		t.Run(contentEncoding, func(te *testing.T) {
			plain := bytes.Repeat([]byte("payload-payload-"), 4096)
			frame := zstdBytes(te, plain)
			if contentEncoding == "gzip" {
				frame = gzipBytes(te, plain)
			}

			for round := range 15 {
				source := &dribbleReader{body: frame, stop: make(chan struct{})}
				reader, err := compression.WrapReader(contentEncoding, source)
				require.NoError(te, err)

				readDone := make(chan struct{})
				go func() {
					defer close(readDone)
					_, _ = io.Copy(io.Discard, reader)
				}()

				time.Sleep(time.Duration(round%7+1) * time.Millisecond)
				close(source.stop) // stands in for closing the response body
				closed := make(chan struct{})
				go func() {
					defer close(closed)
					_ = reader.Close()
				}()
				select {
				case <-closed:
				case <-time.After(10 * time.Second):
					te.Fatal("Close deadlocked against a read still inside the codec")
				}

				other, err := compression.WrapReader(contentEncoding, bytes.NewReader(frame))
				require.NoError(te, err)
				got, err := io.ReadAll(other)
				require.NoError(te, err)
				require.NoError(te, other.Close())
				require.Equal(te, plain, got, "a codec was released while it was still being read")

				<-readDone
			}
		})
	}
}
