package protocol_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeFirstChunkStreamable(t *testing.T) {
	// oversized pads a JSON-RPC envelope so its total length exceeds
	// MaxChunkSize; otherwise Peek reports the whole body is already buffered
	// and AnalyzeFirstChunk refuses to stream regardless of contents.
	oversized := func(prefix, suffix string) []byte {
		pad := bytes.Repeat([]byte("a"), protocol.MaxChunkSize)
		return append(append([]byte(prefix), pad...), []byte(suffix)...)
	}
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
			name:     "full object result in one chunk then no stream",
			body:     []byte(`{"id": 1, "result": {"message": "mess"}}`),
			expected: false,
		},
		{
			name:     "full array result in one chunk then no stream",
			body:     []byte(`{"id": 1, "result": [1, 2, 3]}`),
			expected: false,
		},
		{
			name:     "result larger than chunk without error then stream",
			body:     append([]byte(`{"id": 1, "result": "`), append(bytes.Repeat([]byte("a"), protocol.MaxChunkSize), []byte(`"}`)...)...),
			expected: true,
		},
		{
			name:     "error within the first chunk of an oversized body then no stream",
			body:     append([]byte(`{"id": 1, "error": {"message": "`), append(bytes.Repeat([]byte("e"), protocol.MaxChunkSize), []byte(`"}}`)...)...),
			expected: false,
		},
		{
			// result located, then a trailing error key still visible inside
			// the first chunk -> must not stream (dshackle parity, point 1).
			name:     "trailing error after small result in oversized body then no stream",
			body:     oversized(`{"id":1,"result":null,"error":{"code":-1},"pad":"`, `"}`),
			expected: false,
		},
		{
			// oversized body whose envelope has neither result nor error in the
			// first chunk -> nothing to unwrap, fall back to buffering.
			name:     "no result and no error in oversized body then no stream",
			body:     oversized(`{"id":1,"pad":"`, `"}`),
			expected: false,
		},
		{
			// known limitation: a complete body that is exactly the chunk size
			// reports nil (not io.EOF) from Peek, so it is not recognized as
			// fully buffered and still takes the stream path
			name:     "complete body exactly chunk size then stream",
			body:     exactChunkSizeBody(),
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			reader := bufio.NewReaderSize(bytes.NewReader(test.body), protocol.MaxChunkSize)

			assert.Equal(te, test.expected, protocol.AnalyzeFirstChunk(reader, protocol.MaxChunkSize).Streamable)
		})
	}
}

// exactChunkSizeBody returns a valid JSON-RPC response whose total length is
// exactly protocol.MaxChunkSize bytes.
func exactChunkSizeBody() []byte {
	prefix := []byte(`{"id":1,"result":"`)
	suffix := []byte(`"}`)
	pad := protocol.MaxChunkSize - len(prefix) - len(suffix)
	return append(prefix, append(bytes.Repeat([]byte("a"), pad), suffix...)...)
}

func TestAnalyzeChunkResultStart(t *testing.T) {
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
		{
			// a string value of "error" inside the result must NOT disable
			// streaming - only an envelope-level error key does (the
			// false-negative the single pass fixes).
			name:     "result containing nested error key still streams",
			body:     `{"jsonrpc":"2.0","id":1,"result":{"error":"x","ok":1}}`,
			wantHead: `{"error":"x","ok":1}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			a := protocol.AnalyzeChunk([]byte(test.body))
			require.True(te, a.Streamable)
			require.GreaterOrEqual(te, a.ResultStart, 0)
			assert.Equal(te, test.wantHead, test.body[a.ResultStart:])

			hint := a.Hint()
			require.NotNil(te, hint, "streamable analysis must produce a hint")
			assert.Equal(te, protocol.JsonRpc, hint.Type())
			jsonHint, ok := hint.(protocol.JsonRpcResultStreamHint)
			require.True(te, ok)
			assert.Equal(te, a.ResultStart, jsonHint.ResultStart)
		})
	}
}

func TestAnalyzeChunkTruncatedValue(t *testing.T) {
	// First chunk contains the "result" key but not the full value — the
	// value continues in a follow-up chunk. AnalyzeChunk must still locate the
	// value and prime a counter carrying enough state (depth, inStr, escape)
	// to keep byte-counting across the chunk boundary until the value ends.
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
			a := protocol.AnalyzeChunk([]byte(test.chunk1))
			require.True(te, a.Streamable)
			require.GreaterOrEqual(te, a.ResultStart, 0)
			counter := a.Counter

			var out bytes.Buffer
			done := stepThrough(&out, &counter, test.chunk1[a.ResultStart:])
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

func TestAnalyzeChunkNoResult(t *testing.T) {
	// Every case must report not-streamable with ResultStart == -1: there is
	// no result value to unwrap, so the connector falls back to buffering.
	cases := []struct {
		name string
		body string
	}{
		{name: "error envelope", body: `{"id":1,"error":{"code":-1}}`},
		{name: "no result, no error", body: `{"jsonrpc":"2.0","id":1}`},
		{name: "top-level array", body: `[1,2,3]`},
		{name: "empty buffer", body: ``},
		{name: "garbage", body: `not json`},
		{name: "result key but value byte not in chunk", body: `{"jsonrpc":"2.0","id":1,"result":`},
		{name: "result key with only whitespace after colon", body: `{"jsonrpc":"2.0","id":1,"result":   `},
	}
	for _, c := range cases {
		t.Run(c.name, func(te *testing.T) {
			a := protocol.AnalyzeChunk([]byte(c.body))
			assert.False(te, a.Streamable)
			assert.Equal(te, -1, a.ResultStart)
			assert.Nil(te, a.Hint(), "non-streamable analysis must not produce a hint")
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
			a := protocol.AnalyzeChunk([]byte(env))
			require.GreaterOrEqual(te, a.ResultStart, 0)
			start, counter := a.ResultStart, a.Counter
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

// benchChunk builds an ~8KB first chunk of a large eth_getLogs-style response:
// a streamable array result whose value overflows the chunk. Both the new
// single pass and the old two-pass baseline walk exactly these bytes.
func benchChunk() []byte {
	entry := `{"address":"0x000000000000000000000000000000000000dead","topics":["0xdeadbeef"],"data":"0x01"},`
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":[`)
	for len(body) < protocol.MaxChunkSize {
		body = append(body, entry...)
	}
	return body[:protocol.MaxChunkSize]
}

// BenchmarkAnalyzeChunk measures the new single-pass first-chunk analysis.
func BenchmarkAnalyzeChunk(b *testing.B) {
	chunk := benchChunk()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := protocol.AnalyzeChunk(chunk)
		if !a.Streamable {
			b.Fatal("expected streamable")
		}
	}
}

// BenchmarkOldTwoPass replicates the previous behavior — a full-chunk
// encoding/json scan for an "error" token (old ResponseCanBeStreamed) followed
// by a second decoder walk to find the result start (old FindResultStart) —
// over the same bytes, so the allocation/CPU delta of merging them is visible.
func BenchmarkOldTwoPass(b *testing.B) {
	chunk := benchChunk()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !oldCanBeStreamed(chunk) {
			b.Fatal("expected streamable")
		}
		if _, err := oldFindResultStart(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// oldCanBeStreamed is a copy of the removed ResponseCanBeStreamed body (minus
// the bufio.Peek) used only as a benchmark baseline: it tokenizes the entire
// chunk looking for any "error" string token.
func oldCanBeStreamed(body []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		token, err := dec.Token()
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return false
		}
		if s, ok := token.(string); ok && s == "error" {
			return false
		}
	}
	return true
}

// oldFindResultStart is a copy of the removed FindResultStart used only as a
// benchmark baseline: a second decoder walk locating the result value start.
func oldFindResultStart(buf []byte) (int, error) {
	dec := json.NewDecoder(bytes.NewReader(buf))
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return 0, errors.New("expected json object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, err
		}
		key, _ := keyTok.(string)
		if key != "result" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return 0, err
			}
			continue
		}
		return int(dec.InputOffset()), nil
	}
	return 0, errors.New("result field is missing")
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
