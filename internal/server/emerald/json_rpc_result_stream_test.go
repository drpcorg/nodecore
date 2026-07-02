package emerald

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writerSink adapts a plain io.Writer to the chunkSink interface, ignoring the
// final flag, so tests can assert the extracted result bytes without caring
// about end-of-stream framing.
type writerSink struct{ w io.Writer }

func (s writerSink) WriteChunk(p []byte, _ bool) error {
	_, err := s.w.Write(p)
	return err
}

// streamResult mirrors the production wiring: the connector locates the result
// value via AnalyzeChunk (over the first MaxChunkSize bytes) and the gRPC
// consumer streams from that offset. Tests supply chunk = the bytes the reader
// will yield (or its leading portion for error-injecting readers).
func streamResult(reader io.Reader, out io.Writer, chunk []byte) error {
	if len(chunk) > protocol.MaxChunkSize {
		chunk = chunk[:protocol.MaxChunkSize]
	}
	a := protocol.AnalyzeChunk(chunk)
	return streamJsonRPCResult(context.Background(), reader, writerSink{out}, a.ResultStart, a.Counter)
}

func TestStreamJsonRPCResultExtractsNestedResult(t *testing.T) {
	var out strings.Builder
	input := `{"jsonrpc":"2.0","id":1,"result":{"items":[1,{"k":"v","arr":[true,false,null]}],"s":"x"},"ignored":{"deep":[1,2,3]}}`

	err := streamResult(strings.NewReader(input), &out, []byte(input))
	require.NoError(t, err)
	assert.Equal(t, `{"items":[1,{"k":"v","arr":[true,false,null]}],"s":"x"}`, out.String())
}

func TestStreamJsonRPCResultExtractsPrimitiveResult(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{
			name:     "string result",
			response: `{"jsonrpc":"2.0","id":"1","result":"ok"}`,
			expected: `"ok"`,
		},
		{
			name:     "number result",
			response: `{"jsonrpc":"2.0","id":"1","result":12345}`,
			expected: `12345`,
		},
		{
			name:     "negative number result",
			response: `{"jsonrpc":"2.0","id":"1","result":-42}`,
			expected: `-42`,
		},
		{
			name:     "floating number result",
			response: `{"jsonrpc":"2.0","id":"1","result":3.14159}`,
			expected: `3.14159`,
		},
		{
			name:     "scientific notation result",
			response: `{"jsonrpc":"2.0","id":"1","result":1.5e10}`,
			expected: `1.5e10`,
		},
		{
			name:     "boolean true result",
			response: `{"jsonrpc":"2.0","id":"1","result":true}`,
			expected: `true`,
		},
		{
			name:     "boolean false result",
			response: `{"jsonrpc":"2.0","id":"1","result":false}`,
			expected: `false`,
		},
		{
			name:     "null result",
			response: `{"jsonrpc":"2.0","id":"1","result":null}`,
			expected: `null`,
		},
		{
			name:     "empty string result",
			response: `{"jsonrpc":"2.0","id":"1","result":""}`,
			expected: `""`,
		},
		{
			name:     "string with escaped quote",
			response: `{"jsonrpc":"2.0","id":"1","result":"a\"b"}`,
			expected: `"a\"b"`,
		},
		{
			name:     "string with escaped backslash",
			response: `{"jsonrpc":"2.0","id":"1","result":"a\\b"}`,
			expected: `"a\\b"`,
		},
		{
			name:     "string with unicode escape",
			response: `{"jsonrpc":"2.0","id":"1","result":"ÿA"}`,
			expected: `"ÿA"`,
		},
		{
			name:     "string containing brackets",
			response: `{"jsonrpc":"2.0","id":"1","result":"{[}]"}`,
			expected: `"{[}]"`,
		},
		{
			name:     "string ending with escaped backslash",
			response: `{"jsonrpc":"2.0","id":"1","result":"end\\"}`,
			expected: `"end\\"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(te *testing.T) {
			var out strings.Builder
			err := streamResult(strings.NewReader(tc.response), &out, []byte(tc.response))
			require.NoError(te, err)
			assert.Equal(te, tc.expected, out.String())
		})
	}
}

func TestStreamJsonRPCResultContainerEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{
			name:     "empty object",
			response: `{"jsonrpc":"2.0","id":1,"result":{}}`,
			expected: `{}`,
		},
		{
			name:     "empty array",
			response: `{"jsonrpc":"2.0","id":1,"result":[]}`,
			expected: `[]`,
		},
		{
			name:     "array of strings containing close-bracket",
			response: `{"jsonrpc":"2.0","id":1,"result":["]","["]}`,
			expected: `["]","["]`,
		},
		{
			name:     "deeply nested object",
			response: `{"jsonrpc":"2.0","id":1,"result":{"a":{"b":{"c":{"d":{"e":1}}}}}}`,
			expected: `{"a":{"b":{"c":{"d":{"e":1}}}}}`,
		},
		{
			name:     "array of mixed scalars",
			response: `{"jsonrpc":"2.0","id":1,"result":[1,"two",true,null,3.14]}`,
			expected: `[1,"two",true,null,3.14]`,
		},
		{
			name:     "object whose value contains escape inside string with bracket",
			response: `{"jsonrpc":"2.0","id":1,"result":{"k":"v\"]}{["}}`,
			expected: `{"k":"v\"]}{["}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(te *testing.T) {
			var out strings.Builder
			err := streamResult(strings.NewReader(tc.response), &out, []byte(tc.response))
			require.NoError(te, err)
			assert.Equal(te, tc.expected, out.String())
		})
	}
}

func TestStreamJsonRPCResultEnvelopeShape(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected string
	}{
		{
			name:     "result is the only key",
			response: `{"result":[1,2,3]}`,
			expected: `[1,2,3]`,
		},
		{
			name:     "result is the first key",
			response: `{"result":42,"jsonrpc":"2.0","id":1}`,
			expected: `42`,
		},
		{
			name:     "result is the last key",
			response: `{"jsonrpc":"2.0","id":1,"result":42}`,
			expected: `42`,
		},
		{
			name:     "result preceded by several ignored keys",
			response: `{"jsonrpc":"2.0","id":1,"meta":{"a":1,"b":[1,2]},"trace":"x","result":"ok"}`,
			expected: `"ok"`,
		},
		{
			name:     "whitespace formatted envelope",
			response: "{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 1,\n  \"result\": {\n    \"a\": 1\n  }\n}",
			expected: "{\n    \"a\": 1\n  }",
		},
		{
			name:     "tabs and CRLF in envelope",
			response: "{\r\n\t\"jsonrpc\":\"2.0\",\r\n\t\"id\":1,\r\n\t\"result\":\"ok\"\r\n}",
			expected: `"ok"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(te *testing.T) {
			var out strings.Builder
			err := streamResult(strings.NewReader(tc.response), &out, []byte(tc.response))
			require.NoError(te, err)
			assert.Equal(te, tc.expected, out.String())
		})
	}
}

func TestStreamJsonRPCResultLargeResultSpanningChunks(t *testing.T) {
	// Build a result whose array is much bigger than MaxChunkSize so the
	// streamer has to read past the buffered prefix and keep byte-counting.
	inner := strings.Repeat(`{"x":1},`, 4096) + `{"x":1}`
	body := `{"jsonrpc":"2.0","id":1,"result":[` + inner + `],"trailing":"ignored"}`

	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, "["+inner+"]", out.String())
}

func TestStreamJsonRPCResultStringResultSpansChunks(t *testing.T) {
	// A single large string whose closing quote falls well past
	// MaxChunkSize; escape state must carry correctly across chunks.
	payload := strings.Repeat(`x`, 16384) + `\"` + strings.Repeat(`y`, 4096)
	body := `{"jsonrpc":"2.0","id":1,"result":"` + payload + `"}`

	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, `"`+payload+`"`, out.String())
}

func TestStreamJsonRPCResultScalarSpansChunks(t *testing.T) {
	// Pathological but legal: a number whose digits stretch past the
	// first-chunk boundary. Scalar counter must keep going until it sees a
	// delimiter byte.
	digits := strings.Repeat("9", 20000)
	body := `{"jsonrpc":"2.0","id":1,"result":` + digits + `}`

	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, digits, out.String())
}

func TestStreamJsonRPCResultDrainsTrailingEnvelope(t *testing.T) {
	// After the result value ends, the streamer should still read through
	// the rest of the body so the wrapping CloseReader observes EOF and
	// closes resp.Body. countingReader verifies the bytes after the result
	// were actually read.
	body := `{"jsonrpc":"2.0","id":1,"result":42,"ignored":"tail","more":[1,2,3]}`
	cr := &countingReader{src: strings.NewReader(body)}

	var out strings.Builder
	err := streamResult(cr, &out, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, `42`, out.String())
	assert.Equal(t, len(body), cr.bytesRead, "entire body should be read so the upstream body closer fires")
}

func TestStreamJsonRPCResultHandlesSlowReader(t *testing.T) {
	// A reader that hands out one byte at a time forces the read-ahead
	// continuation to iterate many times. The output must still be byte-exact.
	inner := strings.Repeat(`{"x":1},`, 2000) + `{"x":1}`
	body := `{"jsonrpc":"2.0","id":1,"result":[` + inner + `]}`
	want := "[" + inner + "]"

	var out strings.Builder
	err := streamResult(iotest1ByteReader(strings.NewReader(body)), &out, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, want, out.String())
}

func TestStreamJsonRPCResultMissingResult(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":"1","error":{"code":-1}}`
	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result field is missing")
}

func TestStreamJsonRPCResultInvalidTopLevel(t *testing.T) {
	// A non-object top level can't yield a result value, so AnalyzeChunk
	// reports no result and the consumer rejects it with "result field is
	// missing" (in production the connector would have buffered it instead).
	body := `[{"result":1}]`
	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result field is missing")
}

func TestStreamJsonRPCResultEmptyBody(t *testing.T) {
	var out strings.Builder
	err := streamResult(strings.NewReader(``), &out, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
}

func TestStreamJsonRPCResultGarbage(t *testing.T) {
	body := `not json at all`
	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
}

func TestStreamJsonRPCResultInvalidJSON(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":"1","result":{"a":1`
	var out strings.Builder
	err := streamResult(strings.NewReader(body), &out, []byte(body))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
}

func TestStreamJsonRPCResultTruncated(t *testing.T) {
	// Every input here is missing the closing bracket / quote of the
	// "result" value. streamJsonRPCResult must return an error that
	// mentions truncation rather than silently emit a partial value.
	//
	// The cases are split between the two code paths that emit this error:
	//   (1) "result value truncated"   — body is smaller than MaxChunkSize
	//                                    so io.ReadFull returns
	//                                    ErrUnexpectedEOF and emitFromBuffer
	//                                    runs out of bytes with the counter
	//                                    still open (`exhausted && !done`
	//                                    branch in streamJsonRPCResult).
	//   (2) "result value truncated"   — body is larger than MaxChunkSize so
	//                                    the read-ahead continuation is engaged
	//                                    and reaches EOF before the counter closes.
	//   (3) "result field is missing"  — AnalyzeChunk sees the "result" key
	//                                    but the chunk ends before any byte of
	//                                    the value, so no result is located
	//                                    and the consumer rejects start == -1.
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "object truncated, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":{"a":1`,
			want: "result value truncated",
		},
		{
			name: "object truncated mid-key, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":{"abc`,
			want: "result value truncated",
		},
		{
			name: "array truncated, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":[1,2,3`,
			want: "result value truncated",
		},
		{
			name: "string truncated mid-content, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":"abcdef`,
			want: "result value truncated",
		},
		{
			name: "string truncated mid-escape, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":"a\`,
			want: "result value truncated",
		},
		{
			name: "scalar truncated, body fits first chunk",
			body: `{"jsonrpc":"2.0","id":1,"result":12345`,
			want: "result value truncated",
		},
		{
			name: "object truncated, body spans many chunks",
			body: `{"jsonrpc":"2.0","id":1,"result":[` + strings.Repeat(`{"x":1},`, 4096),
			want: "result value truncated",
		},
		{
			name: "string truncated, body spans many chunks",
			body: `{"jsonrpc":"2.0","id":1,"result":"` + strings.Repeat(`x`, 20000),
			want: "result value truncated",
		},
		{
			name: "scalar truncated, body spans many chunks",
			body: `{"jsonrpc":"2.0","id":1,"result":` + strings.Repeat(`9`, 20000),
			want: "result value truncated",
		},
		{
			name: "body ends right after ':' before value byte",
			body: `{"jsonrpc":"2.0","id":1,"result":`,
			want: "result field is missing",
		},
		{
			name: "body ends with whitespace after ':' before value byte",
			body: `{"jsonrpc":"2.0","id":1,"result":   `,
			want: "result field is missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(te *testing.T) {
			var out strings.Builder
			err := streamResult(strings.NewReader(tc.body), &out, []byte(tc.body))
			require.Error(te, err)
			assert.Contains(te, err.Error(), tc.want, "got: %s", err.Error())
			assert.Contains(te, err.Error(), "unable to parse stream response")
		})
	}
}

func TestStreamJsonRPCResultTruncatedByReaderEOFMidStream(t *testing.T) {
	// The read-ahead continuation sees io.EOF before the counter closes. This
	// is the network-side variant of the truncation case: bytes arrive then the
	// connection ends cleanly without delivering the full result.
	first := []byte(`{"jsonrpc":"2.0","id":1,"result":[` + strings.Repeat(`{"x":1},`, 2048))
	r := &errReader{data: first, err: io.EOF}

	var out strings.Builder
	err := streamResult(r, &out, first)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
	assert.Contains(t, err.Error(), "result value truncated")
}

func TestStreamJsonRPCResultReaderErrorBeforeFirstChunk(t *testing.T) {
	// io.ReadFull surfaces a non-EOF reader error — streamer must wrap it
	// with the "unable to parse stream response" prefix.
	want := errors.New("upstream connection reset")
	r := &errReader{err: want}

	var out strings.Builder
	err := streamResult(r, &out, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
	assert.ErrorIs(t, err, want)
}

func TestStreamJsonRPCResultReaderErrorDuringStream(t *testing.T) {
	// First chunk parses fine and the result value starts, but a network
	// error cuts the stream mid-body. The streamer must propagate the
	// error rather than emit a silently truncated value.
	want := errors.New("conn closed")
	first := []byte(`{"jsonrpc":"2.0","id":1,"result":[` + strings.Repeat(`{"x":1},`, 1024))
	// Pad first chunk past MaxChunkSize so the read-ahead continuation is engaged.
	first = append(first, []byte(strings.Repeat(`{"x":1},`, 1024))...)
	r := &errReader{data: first, err: want}

	var out strings.Builder
	err := streamResult(r, &out, first)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to parse stream response")
	assert.ErrorIs(t, err, want)
}

func TestStreamJsonRPCResultWriterError(t *testing.T) {
	// Writer failure inside the buffered prefix path must surface as an
	// error from streamJsonRPCResult.
	body := `{"jsonrpc":"2.0","id":1,"result":[1,2,3,4,5]}`
	wantErr := errors.New("downstream pipe broken")
	w := &errWriter{failAt: 2, err: wantErr}

	err := streamResult(strings.NewReader(body), w, []byte(body))
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestStreamJsonRPCResultWriterErrorInStreamingPath(t *testing.T) {
	// Same as above but the failure happens after the read-ahead continuation
	// takes over, i.e. past the first chunk.
	inner := strings.Repeat(`{"x":1},`, 4096) + `{"x":1}`
	body := `{"jsonrpc":"2.0","id":1,"result":[` + inner + `]}`
	wantErr := errors.New("downstream pipe broken")
	// Fail well after the first chunk has been emitted.
	w := &errWriter{failAt: 10000, err: wantErr}

	err := streamResult(strings.NewReader(body), w, []byte(body))
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestStreamJsonRPCResultMarksFinalChunkAcrossReads(t *testing.T) {
	// A result array far larger than streamReadChunkSize so it spans the
	// buffered prefix plus several read-ahead reads. Exactly one emitted chunk
	// — the last — must carry final=true, and the chunks must concatenate back
	// to the full result value.
	inner := strings.Repeat(`{"x":1},`, 20000) + `{"x":1}`
	body := `{"jsonrpc":"2.0","id":1,"result":[` + inner + `],"trailing":"ignored"}`
	require.Greater(t, len(inner), streamReadChunkSize, "result must exceed one read-ahead buffer")

	sink := &recordingSink{}
	chunk := []byte(body)
	if len(chunk) > protocol.MaxChunkSize {
		chunk = chunk[:protocol.MaxChunkSize]
	}
	a := protocol.AnalyzeChunk(chunk)
	err := streamJsonRPCResult(context.Background(), strings.NewReader(body), sink, a.ResultStart, a.Counter)
	require.NoError(t, err)

	require.GreaterOrEqual(t, len(sink.chunks), 2, "large result should span multiple chunks")
	finalCount := 0
	for i, final := range sink.finals {
		if final {
			finalCount++
			assert.Equal(t, len(sink.finals)-1, i, "the final flag must land on the last chunk")
		}
	}
	assert.Equal(t, 1, finalCount, "exactly one chunk should be marked final")

	var got []byte
	for _, c := range sink.chunks {
		got = append(got, c...)
	}
	assert.Equal(t, "["+inner+"]", string(got))
}

func TestStreamReadAheadReturnsOnProcessError(t *testing.T) {
	// The reader never reaches EOF, so without the early-return + ctx cancel the
	// producer would block forever once the channel fills. streamReadAhead must
	// surface the process error and return promptly (a leak would hang the test).
	want := errors.New("boom")
	r := infiniteReader{chunk: bytes.Repeat([]byte("a"), 1024)}

	calls := 0
	err := streamReadAhead(context.Background(), r, func(buf []byte) (bool, error) {
		calls++
		if calls >= 2 {
			return false, want
		}
		return false, nil
	})
	require.ErrorIs(t, err, want)
}

func TestStreamReadAheadReturnsWhenProcessObservesCancel(t *testing.T) {
	// Production cancellation path: the reader never reaches EOF, but process
	// (stream.Send) fails once the context is cancelled. streamReadAhead must
	// surface that and return promptly instead of looping on the endless reader.
	ctx, cancel := context.WithCancel(context.Background())
	r := infiniteReader{chunk: bytes.Repeat([]byte("a"), 1024)}

	err := streamReadAhead(ctx, r, func(buf []byte) (bool, error) {
		cancel()
		return false, ctx.Err()
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStreamJsonRPCResultIgnoresTailErrorAfterResultComplete(t *testing.T) {
	// The result value completes, then the upstream errors before the envelope
	// tail arrives (e.g. a reset, or cancellation landing in the drain window).
	// The full value was already emitted, so streamJsonRPCResult must report
	// success rather than turn a delivered response into an error.
	inner := strings.Repeat(`{"x":1},`, 2048) + `{"x":1}` // pushes the result past the 8KB prefix
	body := `{"jsonrpc":"2.0","id":1,"result":[` + inner + `]`
	r := &errReader{data: []byte(body), err: errors.New("conn reset")}

	sink := &recordingSink{}
	chunk := []byte(body)
	if len(chunk) > protocol.MaxChunkSize {
		chunk = chunk[:protocol.MaxChunkSize]
	}
	a := protocol.AnalyzeChunk(chunk)
	err := streamJsonRPCResult(context.Background(), r, sink, a.ResultStart, a.Counter)
	require.NoError(t, err)

	require.NotEmpty(t, sink.finals)
	assert.True(t, sink.finals[len(sink.finals)-1], "the last emitted chunk must be marked final")
	var got []byte
	for _, c := range sink.chunks {
		got = append(got, c...)
	}
	assert.Equal(t, "["+inner+"]", string(got))
}

func TestStreamReadAheadClosesReaderOnEarlyReturn(t *testing.T) {
	// process fails on the first chunk while the reader would then block forever
	// in its next Read. streamReadAhead must Close the reader on the early return
	// so the parked producer goroutine unblocks deterministically instead of
	// lingering until an upstream/HTTP timeout.
	want := errors.New("send failed")
	r := &blockingCloseReader{data: bytes.Repeat([]byte("a"), 1024), unblock: make(chan struct{})}

	err := streamReadAhead(context.Background(), r, func(buf []byte) (bool, error) {
		return false, want
	})
	require.ErrorIs(t, err, want)

	select {
	case <-r.unblock:
		// Close was called, which releases the producer's blocked Read.
	default:
		t.Fatal("reader was not closed on early return; the producer goroutine would leak")
	}
}

func TestStreamReadAheadStopsAfterDone(t *testing.T) {
	// Once process reports done, streamReadAhead must keep draining the reader to
	// EOF (so the CloseReader fires) but must not invoke process again.
	body := strings.Repeat("x", streamReadChunkSize*3)
	cr := &countingReader{src: strings.NewReader(body)}

	calls := 0
	err := streamReadAhead(context.Background(), cr, func(buf []byte) (bool, error) {
		calls++
		return true, nil // done on the very first chunk
	})
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "process must not be called again after reporting done")
	assert.Equal(t, len(body), cr.bytesRead, "reader must be drained to EOF after done")
}

func BenchmarkStreamJsonRPCResult(b *testing.B) {
	inner := strings.Repeat(`{"address":"0x000000000000000000000000000000000000dead","topics":["0xdeadbeef"],"data":"0x01"},`, 8192)
	inner = inner[:len(inner)-1] // drop trailing comma
	body := []byte(`{"jsonrpc":"2.0","id":1,"result":[` + inner + `]}`)
	chunk := body
	if len(chunk) > protocol.MaxChunkSize {
		chunk = chunk[:protocol.MaxChunkSize]
	}
	a := protocol.AnalyzeChunk(chunk)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := streamJsonRPCResult(context.Background(), bytes.NewReader(body), writerSink{io.Discard}, a.ResultStart, a.Counter); err != nil {
			b.Fatal(err)
		}
	}
}

// --- test helpers -----------------------------------------------------------

// recordingSink captures every chunk (copied, since the streamer aliases its
// read buffer) along with its final flag, so tests can assert framing.
type recordingSink struct {
	chunks [][]byte
	finals []bool
}

func (s *recordingSink) WriteChunk(p []byte, final bool) error {
	s.chunks = append(s.chunks, append([]byte(nil), p...))
	s.finals = append(s.finals, final)
	return nil
}

// infiniteReader yields chunk on every Read and never reaches EOF, used to
// verify the read-ahead loop unwinds on an early return instead of leaking.
type infiniteReader struct{ chunk []byte }

func (r infiniteReader) Read(p []byte) (int, error) {
	return copy(p, r.chunk), nil
}

// blockingCloseReader hands out data once, then blocks in Read until Close is
// called (which closes unblock). It models a slow upstream whose read only
// returns once the body is closed, so tests can assert streamReadAhead closes
// the reader on an early return.
type blockingCloseReader struct {
	data    []byte
	pos     int
	unblock chan struct{}
	once    sync.Once
}

func (r *blockingCloseReader) Read(p []byte) (int, error) {
	if r.pos < len(r.data) {
		n := copy(p, r.data[r.pos:])
		r.pos += n
		return n, nil
	}
	<-r.unblock
	return 0, io.EOF
}

func (r *blockingCloseReader) Close() error {
	r.once.Do(func() { close(r.unblock) })
	return nil
}

// errReader emits the bytes in data, then returns err. If data is empty the
// error is returned on the first Read.
type errReader struct {
	data []byte
	err  error
	pos  int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	if r.pos >= len(r.data) {
		return n, r.err
	}
	return n, nil
}

// errWriter accepts the first failAt bytes then returns err on subsequent
// writes.
type errWriter struct {
	failAt  int
	written int
	err     error
}

func (w *errWriter) Write(p []byte) (int, error) {
	remaining := w.failAt - w.written
	if remaining <= 0 {
		return 0, w.err
	}
	if len(p) <= remaining {
		w.written += len(p)
		return len(p), nil
	}
	w.written += remaining
	return remaining, w.err
}

// countingReader wraps an io.Reader and tracks how many bytes were read,
// used to verify the streamer drains the body after the result value ends.
type countingReader struct {
	src       io.Reader
	bytesRead int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.bytesRead += n
	return n, err
}

// iotest1ByteReader returns a reader that hands out at most one byte per
// Read call. Equivalent to iotest.OneByteReader but avoids the iotest
// import (this package keeps its imports minimal).
func iotest1ByteReader(src io.Reader) io.Reader {
	return &oneByteReader{src: src}
}

type oneByteReader struct{ src io.Reader }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return r.src.Read(p[:1])
}
