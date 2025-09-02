package protocol_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestProcessFirstChunk(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected bool
	}{
		{
			name:     "full body with error then no stream",
			body:     []byte(`{"id": 1, "error": {"message": "err"}}`),
			expected: false,
		},
		{
			name:     "full body with result in one chunk then stream",
			body:     []byte(`{"id": 1, "result": {"message": "mess"}}`),
			expected: true,
		},
		{
			name:     "not full body without error in the first chunk then stream",
			body:     []byte(`{"id": 1, "result": {"message": "mess`),
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			reader := bufio.NewReaderSize(bytes.NewReader(test.body), protocol.MaxChunkSize)

			canBeStreamed := protocol.ResponseCanBeStreamed(reader, protocol.MaxChunkSize)

			assert.Equal(t, test.expected, canBeStreamed)
		})
	}
}

func TestCloseReaderCloseOnEOF(t *testing.T) {
	mainReader := bytes.NewReader([]byte("superText superText superText 12123123"))
	closerReader := newReaderMock()
	closeReader := protocol.NewCloseReader(context.Background(), mainReader, closerReader)

	closerReader.On("Close").Return(nil)

	buf := make([]byte, 8)
	var err error
	for {
		_, err = closeReader.Read(buf)
		if err == io.EOF {
			break
		}
	}
	closerReader.AssertCalled(t, "Close")
	assert.True(t, err == io.EOF)
}

func TestCloseReaderCloseOnAnyError(t *testing.T) {
	closerReader := newReaderMock()
	closeReader := protocol.NewCloseReader(context.Background(), closerReader, closerReader)

	closerReader.On("Close").Return(nil)
	closerReader.On("Read", mock.Anything).Return(0, errors.New("myError"))

	buf := make([]byte, 8)
	var err error
	for {
		_, err = closeReader.Read(buf)
		if err != nil {
			break
		}
	}
	closerReader.AssertExpectations(t)
	assert.True(t, err != nil)
}

type readerCloserMock struct {
	mock.Mock
}

func newReaderMock() *readerCloserMock {
	return &readerCloserMock{}
}

func (r *readerCloserMock) Read(p []byte) (n int, err error) {
	args := r.Called(p)
	if args.Get(1) == nil {
		err = nil
	} else {
		err = args.Get(1).(error)
	}
	return args.Get(0).(int), err
}

func (r *readerCloserMock) Close() error {
	args := r.Called()
	var err error
	if args.Get(0) == nil {
		err = nil
	} else {
		err = args.Get(0).(error)
	}
	return err
}
