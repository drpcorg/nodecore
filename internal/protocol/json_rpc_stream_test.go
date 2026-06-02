package protocol_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
		{
			name:     "error key anywhere in envelope then no stream",
			body:     []byte(`{"id": 1, "result": null, "error": {"code": -1}}`),
			expected: false,
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

func TestFindResultStart(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantHead string // first few bytes of result (buf[start:])
	}{
		{
			name:     "object result",
			body:     `{"jsonrpc":"2.0","id":1,"result":{"a":1}}`,
			wantHead: `{"a":1}}`,
		},
		{
			name:     "array result",
			body:     `{"jsonrpc":"2.0","id":1,"result":[1,2,3]}`,
			wantHead: `[1,2,3]}`,
		},
		{
			name:     "string result",
			body:     `{"jsonrpc":"2.0","id":1,"result":"ok"}`,
			wantHead: `"ok"}`,
		},
		{
			name:     "scalar number result",
			body:     `{"jsonrpc":"2.0","id":1,"result":42}`,
			wantHead: `42}`,
		},
		{
			name:     "scalar null result",
			body:     `{"jsonrpc":"2.0","id":1,"result":null}`,
			wantHead: `null}`,
		},
		{
			name:     "whitespace around value",
			body:     `{"jsonrpc":"2.0","id":1,"result"  :  {"a":1}}`,
			wantHead: `{"a":1}}`,
		},
		{
			name:     "result is first key",
			body:     `{"result":[1,2]}`,
			wantHead: `[1,2]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			start, _, err := protocol.FindResultStart([]byte(test.body))
			require.NoError(te, err)
			assert.Equal(te, test.wantHead, test.body[start:])
		})
	}
}

func TestFindResultStartTruncatedValue(t *testing.T) {
	// First chunk contains the "result" key but not the full value — the
	// value continues in a follow-up chunk. FindResultStart must still
	// succeed on the first chunk and the counter must carry enough state
	// (depth, inStr, escape) to keep byte-counting across the chunk
	// boundary until the value really ends.
	tests := []struct {
		name   string
		chunk1 string
		chunk2 string
		want   string
	}{
		{
			name:   "object truncated inside nested array",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":{"big":[1,2`,
			chunk2: `,3,4]}}`,
			want:   `{"big":[1,2,3,4]}`,
		},
		{
			name:   "object truncated mid-key",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":{"abc`,
			chunk2: `def":1}}`,
			want:   `{"abcdef":1}`,
		},
		{
			name:   "array truncated mid-element",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":[1,2,3`,
			chunk2: `,4,5]}`,
			want:   `[1,2,3,4,5]`,
		},
		{
			name:   "string truncated mid-content",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":"abcdef`,
			chunk2: `ghij"}`,
			want:   `"abcdefghij"`,
		},
		{
			name:   "string truncated mid-escape so escape state carries",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":"a\`,
			chunk2: `"b"}`,
			want:   `"a\"b"`,
		},
		{
			name:   "scalar number truncated",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":123`,
			chunk2: `456}`,
			want:   `123456`,
		},
		{
			name:   "object truncated inside string value",
			chunk1: `{"jsonrpc":"2.0","id":1,"result":{"k":"unfini`,
			chunk2: `shed"}}`,
			want:   `{"k":"unfinished"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			start, counter, err := protocol.FindResultStart([]byte(test.chunk1))
			require.NoError(te, err)

			var out bytes.Buffer
			done := stepThrough(&out, &counter, test.chunk1[start:])
			require.False(te, done, "value should not have ended in the first chunk")
			done = stepThrough(&out, &counter, test.chunk2)
			require.True(te, done, "value should end inside the second chunk")
			assert.Equal(te, test.want, out.String())
		})
	}
}

// stepThrough feeds chunk to counter byte-by-byte, appending the bytes that
// belong to the result value to out. Returns true once the counter signals
// the end of the value.
func stepThrough(out *bytes.Buffer, counter *protocol.ResultCounter, chunk string) bool {
	flush := 0
	for i := 0; i < len(chunk); i++ {
		switch counter.Step(chunk[i]) {
		case protocol.StepContinue:
			continue
		case protocol.StepFinishHere:
			out.WriteString(chunk[flush : i+1])
			return true
		case protocol.StepStopBefore:
			out.WriteString(chunk[flush:i])
			return true
		}
	}
	out.WriteString(chunk[flush:])
	return false
}

func TestFindResultStartErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // substring expected in err
	}{
		{name: "error envelope", body: `{"id":1,"error":{"code":-1}}`, want: "result field is missing"},
		{name: "no result, no error", body: `{"jsonrpc":"2.0","id":1}`, want: "result field is missing"},
		{name: "top-level array", body: `[1,2,3]`, want: "expected json object"},
		{name: "empty buffer", body: ``, want: "unable to parse stream response"},
		{name: "garbage", body: `not json`, want: "unable to parse stream response"},
	}
	for _, c := range cases {
		t.Run(c.name, func(te *testing.T) {
			_, _, err := protocol.FindResultStart([]byte(c.body))
			require.Error(te, err)
			assert.Contains(te, err.Error(), c.want)
		})
	}
}

func TestResultCounter(t *testing.T) {
	// Each case feeds the bytes of `result` one at a time and asserts the
	// counter emits the expected sequence of result bytes — the same bytes
	// the streaming output should write.
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object", body: `{"a":1}`, want: `{"a":1}`},
		{name: "nested objects", body: `{"a":{"b":{"c":1}}}xtra`, want: `{"a":{"b":{"c":1}}}`},
		{name: "object containing array", body: `{"arr":[1,2,3]}rest`, want: `{"arr":[1,2,3]}`},
		{name: "array of objects", body: `[{"a":1},{"b":2}],rest`, want: `[{"a":1},{"b":2}]`},
		{name: "string with brackets inside", body: `{"a":"]}{["}rest`, want: `{"a":"]}{["}`},
		{name: "string with escaped quote", body: `{"a":"x\"y"}rest`, want: `{"a":"x\"y"}`},
		{name: "string with escaped backslash", body: `{"a":"x\\"}rest`, want: `{"a":"x\\"}`},
		{name: "top-level string with escaped quote", body: `"a\"b"rest`, want: `"a\"b"`},
		{name: "scalar terminated by comma", body: `12345,more`, want: `12345`},
		{name: "scalar terminated by close brace", body: `true}`, want: `true`},
		{name: "scalar terminated by whitespace", body: `false }`, want: `false`},
		{name: "scalar null", body: `null}`, want: `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			env := `{"result":` + test.body + `}`
			start, counter, err := protocol.FindResultStart([]byte(env))
			require.NoError(te, err)
			var out bytes.Buffer
			flush := start
			for i := start; i < len(env); i++ {
				switch counter.Step(env[i]) {
				case protocol.StepContinue:
					continue
				case protocol.StepFinishHere:
					out.Write([]byte(env[flush : i+1]))
					i = len(env) // break outer
				case protocol.StepStopBefore:
					out.Write([]byte(env[flush:i]))
					i = len(env)
				}
			}
			assert.Equal(te, test.want, out.String())
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
