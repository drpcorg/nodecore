package compression

import (
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"

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
