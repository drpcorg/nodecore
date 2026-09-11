package compression_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/compression"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireWriterRoundTrips(t *testing.T) {
	plain := []byte(`{"jsonrpc":"2.0","id":1,"result":{"number":"0x1337"}}`)

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd} {
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
	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd} {
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

	for _, scheme := range []compression.Scheme{compression.Gzip, compression.Zstd} {
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
