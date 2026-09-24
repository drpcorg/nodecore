package compression_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireWriterRoundTrips(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x1337"}}`)

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd, compression.Brotli} {
		t.Run(string(scheme), func(te *testing.T) {
			var buf bytes.Buffer
			writer, err := compression.AcquireWriter(scheme, &buf)
			require.NoError(te, err)

			_, err = writer.Write(plain)
			require.NoError(te, err)
			require.NoError(te, writer.Close())
			compression.ReleaseWriter(writer)

			reader, err := compression.WrapReader(string(scheme), &buf)
			require.NoError(te, err)
			defer func() { require.NoError(te, reader.Close()) }()
			got, err := io.ReadAll(reader)

			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

// Identity has no encoder. Asking for one is a caller bug - the middleware
// must decide not to compress before it reaches for a writer - so it fails
// loudly rather than silently handing back a passthrough.
func TestAcquireWriterRejectsIdentity(t *testing.T) {
	_, err := compression.AcquireWriter(compression.Identity, io.Discard)

	assert.ErrorIs(t, err, compression.ErrUnsupportedEncoding)
}

// Encoders are pooled, so one released mid-stream must not leak its state
// into the next response that picks it up.
func TestAcquireWriterIsReusableAfterRelease(t *testing.T) {
	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd, compression.Brotli} {
		t.Run(string(scheme), func(te *testing.T) {
			for _, plain := range [][]byte{[]byte("first response"), []byte("an entirely different second response")} {
				var buf bytes.Buffer
				writer, err := compression.AcquireWriter(scheme, &buf)
				require.NoError(te, err)
				_, err = writer.Write(plain)
				require.NoError(te, err)
				require.NoError(te, writer.Close())
				compression.ReleaseWriter(writer)

				reader, err := compression.WrapReader(string(scheme), &buf)
				require.NoError(te, err)
				got, err := io.ReadAll(reader)
				require.NoError(te, err)
				require.NoError(te, reader.Close())

				assert.Equal(te, plain, got)
			}
		})
	}
}

// Streaming responses are flushed chunk by chunk: whatever has been written
// must be decodable by the client before the stream is closed, otherwise a
// subscription-style response would stall until it ended.
func TestWriterFlushDeliversDecodableBytes(t *testing.T) {
	plain := []byte(`{"chunk":"first"}`)

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd, compression.Brotli} {
		t.Run(string(scheme), func(te *testing.T) {
			var buf bytes.Buffer
			writer, err := compression.AcquireWriter(scheme, &buf)
			require.NoError(te, err)
			defer compression.ReleaseWriter(writer)

			_, err = writer.Write(plain)
			require.NoError(te, err)
			require.NoError(te, writer.Flush())

			reader, err := compression.WrapReader(string(scheme), bytes.NewReader(buf.Bytes()))
			require.NoError(te, err)
			defer func() { _ = reader.Close() }()
			got := make([]byte, len(plain))
			_, err = io.ReadFull(reader, got)

			require.NoError(te, err)
			assert.Equal(te, plain, got)
		})
	}
}

// The pooled brotli encoder runs at a 256KiB window, the zstd encoder's. A
// stream's first byte says which window it was written with, so that is what
// pins the setting. (At quality 1 the encoder would not announce less than
// lgwin 18 anyway; asking for 18 is what keeps it from announcing more.)
func TestBrotliWriterDeclaresA256KiBWindow(t *testing.T) {
	var buf bytes.Buffer
	writer, err := compression.AcquireWriter(compression.Brotli, &buf)
	require.NoError(t, err)
	_, err = writer.Write(lowRedundancy(1 << 20))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	compression.ReleaseWriter(writer)

	assert.Equal(t, 18, brotliWindowBits(buf.Bytes()[0]))
}

// failingWriter is a client that has gone away. It lets through the first
// accept bytes - a connection that drops partway through a response - and
// fails every write from then on.
type failingWriter struct {
	accept int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if len(p) <= w.accept {
		w.accept -= len(p)
		return len(p), nil
	}
	n := w.accept
	w.accept = 0
	return n, errors.New("the client went away")
}

// A client that hangs up mid-response leaves its encoder with a failed write
// behind it, and the pool hands that encoder to the next response. Where the
// failure surfaces depends on how much the codec had buffered - Write for a
// large body, Flush or Close for a small one - and the two Resets that
// ReleaseWriter and AcquireWriter do between responses have to clear it
// wherever it came from. They are applied to the same instance here, so the
// test does not depend on which writer the pool happens to hand back.
func TestAcquireWriterIsHealthyAfterAFailedWrite(t *testing.T) {
	failures := []struct {
		name   string
		accept int
		fail   func(compression.Writer) error
	}{
		{"write fails before anything reaches the client", 0, func(w compression.Writer) error {
			_, err := w.Write(lowRedundancy(1 << 20))
			return err
		}},
		{"write fails partway through the body", 4 << 10, func(w compression.Writer) error {
			_, err := w.Write(lowRedundancy(1 << 20))
			return err
		}},
		{"flush fails", 0, func(w compression.Writer) error {
			if _, err := w.Write([]byte(`{"chunk":"first"}`)); err != nil {
				return err
			}
			return w.Flush()
		}},
		{"close fails", 0, func(w compression.Writer) error {
			if _, err := w.Write([]byte(`{"chunk":"first"}`)); err != nil {
				return err
			}
			return w.Close()
		}},
	}

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd, compression.Brotli} {
		for _, failure := range failures {
			t.Run(string(scheme)+"/"+failure.name, func(te *testing.T) {
				writer, err := compression.AcquireWriter(scheme, &failingWriter{accept: failure.accept})
				require.NoError(te, err)
				defer compression.ReleaseWriter(writer)
				require.Error(te, failure.fail(writer), "the step that should have hit the dead client did not fail")

				// What ReleaseWriter and then AcquireWriter do to a pooled
				// writer, in that order.
				var buf bytes.Buffer
				writer.Reset(io.Discard)
				writer.Reset(&buf)
				_, err = writer.Write([]byte("the next response"))
				require.NoError(te, err)
				require.NoError(te, writer.Close())

				reader, err := compression.WrapReader(string(scheme), &buf)
				require.NoError(te, err)
				got, err := io.ReadAll(reader)
				require.NoError(te, err)
				require.NoError(te, reader.Close())
				assert.Equal(te, "the next response", string(got))
			})
		}
	}
}

// A long streamed response is flushed many times before it ends. Every flush
// has to leave a stream whose prefix decodes to exactly what was written so
// far, and the finished stream has to decode to the whole body.
func TestWriterFlushesRepeatedlyMidBody(t *testing.T) {
	body := lowRedundancy(1 << 20)
	parts := [][]byte{body[:1<<18], body[1<<18 : 1<<19], body[1<<19 : 3<<18], body[3<<18:]}

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd, compression.Brotli} {
		t.Run(string(scheme), func(te *testing.T) {
			var buf bytes.Buffer
			writer, err := compression.AcquireWriter(scheme, &buf)
			require.NoError(te, err)
			defer compression.ReleaseWriter(writer)

			written := 0
			for _, part := range parts {
				_, err = writer.Write(part)
				require.NoError(te, err)
				require.NoError(te, writer.Flush())
				written += len(part)

				prefix, err := compression.WrapReader(string(scheme), bytes.NewReader(bytes.Clone(buf.Bytes())))
				require.NoError(te, err)
				got := make([]byte, written)
				_, err = io.ReadFull(prefix, got)
				require.NoError(te, err, "a flushed prefix does not decode to what was written")
				require.NoError(te, prefix.Close())
				require.Equal(te, body[:written], got)
			}
			require.NoError(te, writer.Close())

			whole, err := compression.WrapReader(string(scheme), &buf)
			require.NoError(te, err)
			got, err := io.ReadAll(whole)
			require.NoError(te, err)
			require.NoError(te, whole.Close())
			assert.Equal(te, body, got)
		})
	}
}
