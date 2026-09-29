package compression

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	brrr "github.com/molecule-man/go-brrr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// pooledReader is what keeps a pooled codec from going back to its pool while
// a Read is still inside it: the streaming paths close a body from another
// goroutine to unblock a producer parked in Read. The black-box tests can only
// catch a premature release where it deadlocks, which is zstd; this pins the
// ownership rule itself, for every codec - nothing is released while a Read is
// parked however many Closes arrive, the release happens exactly once when the
// Read returns, and nothing afterwards releases it again.
func TestPooledReaderReleasesOnlyAfterTheParkedReadReturns(t *testing.T) {
	entered := make(chan struct{})
	unblock := make(chan struct{})
	var releases atomic.Int32
	reader := &pooledReader{
		Reader: readerFunc(func([]byte) (int, error) {
			close(entered)
			<-unblock
			return 0, io.ErrUnexpectedEOF
		}),
		release: func() { releases.Add(1) },
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := reader.Read(make([]byte, 8))
		readDone <- err
	}()
	<-entered

	var closers sync.WaitGroup
	for range 8 {
		closers.Add(1)
		go func() {
			defer closers.Done()
			assert.NoError(t, reader.Close())
		}()
	}
	closers.Wait()
	assert.Zero(t, releases.Load(), "the codec was released while a Read was still inside it")

	close(unblock)
	require.ErrorIs(t, <-readDone, io.ErrUnexpectedEOF)
	assert.EqualValues(t, 1, releases.Load(), "the returning Read must release the codec it was holding")

	require.NoError(t, reader.Close())
	_, err := reader.Read(make([]byte, 8))
	assert.ErrorIs(t, err, fs.ErrClosed)
	assert.EqualValues(t, 1, releases.Load(), "a later Close or Read released the codec a second time")
}

// With no Read in flight the first Close releases at once, and only once.
func TestPooledReaderReleasesOnceOnAnIdleClose(t *testing.T) {
	var releases atomic.Int32
	reader := &pooledReader{
		Reader:  readerFunc(func([]byte) (int, error) { return 0, io.EOF }),
		release: func() { releases.Add(1) },
	}

	require.NoError(t, reader.Close())
	require.NoError(t, reader.Close())

	assert.EqualValues(t, 1, releases.Load())
}

func brotliFixture(t *testing.T, name string) []byte {
	t.Helper()
	stream, err := os.ReadFile(filepath.Join("testdata", "brotli", name))
	require.NoError(t, err)
	return stream
}

// Each branch of RFC 7932 §9.1's WBITS encoding, plus the reserved pattern the
// large-window form opens with.
func TestBrotliStreamWindowBitsDecodesEveryEncoding(t *testing.T) {
	tests := []struct {
		first byte
		lgwin int
	}{
		{0b0000_0000, 16},
		{0b0000_0011, 18},
		{0b0000_1111, 24},
		{0b0000_0001, 17},
		{0b0010_0001, 10},
		{0b0111_0001, 15},
		{0b0001_0001, 0},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.lgwin, brotliStreamWindowBits(tt.first), "first byte %#08b", tt.first)
	}
	assert.Equal(t, 0, brotliStreamWindowBits(brotliFixture(t, "response.large-window.br")[0]))
}

// What a pooled brotli decoder is worth pooling for is the tables and buffers
// a decode builds, and go-brrr keeps those across Reset but not across Close.
// So a decoder released from a window small enough to park decodes the next
// body without allocating at all, and one released from a larger window is
// Closed and starts cold - which is what keeps a crafted lgwin 24 stream from
// leaving 32MiB parked in the pool.
func TestReleasedBrotliReaderStaysWarmOnlyForSmallWindows(t *testing.T) {
	tests := []struct {
		file string
		warm bool
	}{
		{"response.lgwin10.br", true},
		{"response.lgwin22.br", true},
		{"response.lgwin24.br", false},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(te *testing.T) {
			stream := brotliFixture(te, tt.file)
			lgwin := brotliStreamWindowBits(stream[0])
			decoder := brrr.NewReader(nil)
			source := bytes.NewReader(nil)
			var decodeErr error
			decode := func() {
				source.Reset(stream)
				decoder.Reset(source)
				if _, err := io.Copy(io.Discard, decoder); err != nil {
					decodeErr = err
				}
				resetBrotliReader(decoder, lgwin)
			}
			decode()

			allocs := testing.AllocsPerRun(20, decode)

			require.NoError(te, decodeErr)
			if tt.warm {
				assert.Zero(te, allocs, "a decoder parked warm rebuilt its state")
			} else {
				assert.NotZero(te, allocs, "a decoder released from a large window kept its state")
			}
		})
	}
}

// A decoder parked warm carries the last body's ring buffer and tables into the
// next one, and Reset has to make that invisible: whatever came before - a
// larger window, a smaller one, a stream abandoned halfway, one that failed to
// decode - the next body decodes to exactly itself.
func TestWarmBrotliReaderDecodesTheNextBodyCleanly(t *testing.T) {
	plain, err := os.ReadFile(filepath.Join("testdata", "brotli", "response.json"))
	require.NoError(t, err)
	other := []byte(`{"jsonrpc":"2.0","id":7,"result":"0x1f"}`)
	otherStream, err := brrr.Compress(other, 5)
	require.NoError(t, err)
	lgwin22 := brotliFixture(t, "response.lgwin22.br")
	lgwin10 := brotliFixture(t, "response.lgwin10.br")

	decoder := brrr.NewReader(nil)
	decodeAll := func(stream []byte) ([]byte, error) {
		defer resetBrotliReader(decoder, brotliStreamWindowBits(stream[0]))
		decoder.Reset(bytes.NewReader(stream))
		return io.ReadAll(decoder)
	}
	decodePart := func(stream []byte, n int) {
		defer resetBrotliReader(decoder, brotliStreamWindowBits(stream[0]))
		decoder.Reset(bytes.NewReader(stream))
		_, err := io.ReadFull(decoder, make([]byte, n))
		require.NoError(t, err)
	}

	got, err := decodeAll(lgwin22)
	require.NoError(t, err)
	assert.Equal(t, plain, got, "the first body")

	got, err = decodeAll(lgwin10)
	require.NoError(t, err)
	assert.Equal(t, plain, got, "a smaller window after a larger one")

	decodePart(lgwin22, 100)
	got, err = decodeAll(otherStream)
	require.NoError(t, err)
	assert.Equal(t, other, got, "a body after one abandoned halfway")

	_, err = decodeAll(append([]byte{lgwin22[0]}, `{"jsonrpc":"2.0"}`...))
	require.Error(t, err)
	got, err = decodeAll(lgwin22)
	require.NoError(t, err)
	assert.Equal(t, plain, got, "a body after one that failed to decode")
}
