package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/rs/zerolog"
)

const MaxChunkSize = 8192

// StreamHint is a marker for the analysis attached to a streaming response,
// produced by the connector's first-chunk pass and consumed by the matching
// streaming path. Type reports which response shape the hint describes; a
// consumer asserts to the concrete hint of that type to read its fields. A nil
// StreamHint means no hint is available (a non-streaming or REST response).
type StreamHint interface {
	Type() RequestType
}

// JsonRpcResultStreamHint is the StreamHint for unwrapping the "result" value
// of a JSON-RPC envelope: where the value begins in the first chunk and a
// counter primed for its JSON type so the consumer can detect where it ends.
type JsonRpcResultStreamHint struct {
	ResultStart int
	Counter     ResultCounter
}

func (JsonRpcResultStreamHint) Type() RequestType { return JsonRpc }

// FirstChunkAnalysis is the result of inspecting the first chunk of a
// JSON-RPC response in a single pass. It both decides whether the response
// may be streamed and, for the gRPC result-unwrap path, hands the consumer
// the offset and primed counter for the "result" value so it does not have
// to re-scan the chunk.
type FirstChunkAnalysis struct {
	// Streamable is true iff a "result" value was located AND no
	// envelope-level "error" key was seen in the first chunk.
	Streamable bool
	// ResultStart is the byte offset of the "result" value within the first
	// chunk, or -1 if none was found.
	ResultStart int
	// Counter is primed for the "result" value's JSON type (valid only when
	// Streamable).
	Counter ResultCounter
}

// Hint returns a StreamHint to attach to a streaming response, or nil when the
// analysis found nothing streamable (so the consumer must not try to unwrap).
func (a FirstChunkAnalysis) Hint() StreamHint {
	if !a.Streamable {
		return nil
	}
	return JsonRpcResultStreamHint{ResultStart: a.ResultStart, Counter: a.Counter}
}

// AnalyzeFirstChunk peeks the first chunk of a JSON-RPC response and inspects
// it in a single json.Decoder walk (mirroring dshackle's parseFirstPart):
//   - an envelope-level "error" key means the response must NOT be streamed
//     (the buffered path parses it so the flow can fail over);
//   - the "result" value's start offset and JSON type are recorded so the
//     gRPC consumer can emit it without a second scan;
//   - after "result" is located its value is skipped and the rest of the
//     chunk is still scanned so a trailing "error" key is caught too.
//
// The walk tolerates a truncated chunk (the result value routinely overflows
// MaxChunkSize): a token error simply ends the walk.
func AnalyzeFirstChunk(reader *bufio.Reader, chunkSize int) FirstChunkAnalysis {
	body, err := reader.Peek(chunkSize)
	if err != nil {
		// Fewer than chunkSize bytes total: the whole response is already
		// buffered in the first chunk, so there is nothing to stream - let the
		// buffered path parse/validate/cache it. Any other Peek error also
		// means we shouldn't stream.
		return FirstChunkAnalysis{ResultStart: -1}
	}
	return AnalyzeChunk(body)
}

// AnalyzeChunk performs the single-pass inspection of an already-materialized
// first chunk. AnalyzeFirstChunk wraps it with a bufio Peek; it is exported so
// the gRPC consumer's tests can locate a result value in a chunk without a
// reader. See AnalyzeFirstChunk for the walk's semantics.
func AnalyzeChunk(buf []byte) FirstChunkAnalysis {
	res := FirstChunkAnalysis{ResultStart: -1}
	dec := json.NewDecoder(bytes.NewReader(buf))
	tok, err := dec.Token()
	if err != nil {
		return res
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return res
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			// Truncated or malformed envelope - stop walking.
			break
		}
		key, ok := keyTok.(string)
		if !ok {
			return FirstChunkAnalysis{ResultStart: -1}
		}
		if key == "error" {
			// Envelope-level error: do not stream, regardless of result.
			return FirstChunkAnalysis{ResultStart: -1}
		}
		if key == "result" {
			start, counter, ok := resultValueStart(buf, int(dec.InputOffset()))
			if !ok {
				return FirstChunkAnalysis{ResultStart: -1}
			}
			res.ResultStart = start
			res.Counter = counter
			// Fast path: skip the result value with the cheap byte counter
			// (no json tokenization / allocation of the result body). If it
			// does not end within the chunk, the value overflows it and a
			// trailing error cannot follow here, so the response is streamable.
			// This is the common case for the large responses streaming exists
			// for, and it avoids walking the (huge) result body entirely.
			if _, done := skipResultValueBytes(buf, start, counter); !done {
				res.Streamable = true
				return res
			}
			// The result value fits inside the chunk (rare for a streamed
			// response). Let the decoder skip it so the walk continues and a
			// trailing "error" key after it is still caught; dec is untouched
			// by the byte scan above, so it is still positioned at the value.
			if err := skipNextValue(dec); err != nil {
				break
			}
			continue
		}
		if err := skipNextValue(dec); err != nil {
			break
		}
	}
	res.Streamable = res.ResultStart >= 0
	return res
}

// counterKind selects how ResultCounter interprets bytes.
type counterKind uint8

const (
	counterObject counterKind = iota + 1
	counterArray
	counterString
	counterScalar
)

// ResultCounter is a byte-level state machine that tracks how much of a
// JSON value's structure is still open.
//
// For object/array kinds, the counter ignores brackets inside string
// literals and respects '\\' escapes. For string kind, it tracks escapes.
// For scalar kind (null / true / false / number), it terminates on the
// first byte that cannot belong to the scalar (',', '}', ']', or whitespace).
type ResultCounter struct {
	kind   counterKind
	depth  int
	inStr  bool
	escape bool
}

// StepResult tells the caller what to do with the byte just fed to Step.
type StepResult uint8

const (
	// StepContinue byte is part of the result; keep reading.
	StepContinue StepResult = iota
	// StepFinishHere byte is the last byte of the result (e.g. closing
	// '}', ']', or '"'); include it, then stop.
	StepFinishHere
	// StepStopBefore byte is NOT part of the result (scalar delimiter);
	// do not include it; stop.
	StepStopBefore
)

// Step feeds one byte into the counter and returns what the caller should
// do with that byte.
func (c *ResultCounter) Step(b byte) StepResult {
	switch c.kind {
	case counterObject:
		return c.stepBracket(b, '{', '}')
	case counterArray:
		return c.stepBracket(b, '[', ']')
	case counterString:
		return c.stepString(b)
	case counterScalar:
		return c.stepScalar(b)
	}
	return StepFinishHere
}

func (c *ResultCounter) stepBracket(b byte, open, close byte) StepResult {
	if c.inStr {
		if c.escape {
			c.escape = false
			return StepContinue
		}
		switch b {
		case '\\':
			c.escape = true
		case '"':
			c.inStr = false
		}
		return StepContinue
	}
	switch b {
	case '"':
		c.inStr = true
	case open:
		c.depth++
	case close:
		if c.depth > 0 {
			c.depth--
			if c.depth == 0 {
				return StepFinishHere
			}
		}
	}
	return StepContinue
}

func (c *ResultCounter) stepString(b byte) StepResult {
	if c.escape {
		c.escape = false
		return StepContinue
	}
	if b == '\\' {
		c.escape = true
		return StepContinue
	}
	if b == '"' {
		if !c.inStr {
			c.inStr = true
			return StepContinue
		}
		return StepFinishHere
	}
	return StepContinue
}

func (c *ResultCounter) stepScalar(b byte) StepResult {
	switch b {
	case ',', '}', ']', ' ', '\t', '\n', '\r':
		return StepStopBefore
	}
	return StepContinue
}

// resultValueStart locates the first byte of the "result" value within buf.
// offsetAfterKey is dec.InputOffset() taken right after the "result" key
// token (it points just past the closing quote of the key string). It then
// advances past whitespace, the ':' separator, and any following whitespace,
// and classifies the value's JSON type to prime a ResultCounter. ok is false
// if the value can't be located/classified (e.g. the chunk is truncated
// right after the key or the ':').
//
// The value itself is NOT parsed — its first byte is enough to classify the
// JSON type and initialize the counter.
func resultValueStart(buf []byte, offsetAfterKey int) (start int, counter ResultCounter, ok bool) {
	p := offsetAfterKey
	for p < len(buf) && isJSONWhitespace(buf[p]) {
		p++
	}
	if p >= len(buf) || buf[p] != ':' {
		return 0, ResultCounter{}, false
	}
	p++
	for p < len(buf) && isJSONWhitespace(buf[p]) {
		p++
	}
	if p >= len(buf) {
		return 0, ResultCounter{}, false
	}
	switch buf[p] {
	case '{':
		counter = ResultCounter{kind: counterObject}
	case '[':
		counter = ResultCounter{kind: counterArray}
	case '"':
		counter = ResultCounter{kind: counterString}
	default:
		counter = ResultCounter{kind: counterScalar}
	}
	return p, counter, true
}

// skipResultValueBytes byte-counts the result value in buf starting at start,
// using a copy of the primed counter so the caller's counter stays fresh. It
// returns the offset just past the value's end and done=true if the value
// terminates within buf, or (len(buf), false) if the value overflows the
// chunk. No JSON tokens are allocated.
func skipResultValueBytes(buf []byte, start int, counter ResultCounter) (end int, done bool) {
	for i := start; i < len(buf); i++ {
		switch counter.Step(buf[i]) {
		case StepContinue:
			continue
		case StepFinishHere:
			return i + 1, true
		case StepStopBefore:
			return i, true
		}
	}
	return len(buf), false
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// skipNextValue consumes the next JSON value from dec without retaining it.
// It assumes the value's opening token has not yet been read.
func skipNextValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipNextValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipNextValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}
	return nil
}

type CloseReader struct {
	readerToClose io.ReadCloser
	mainReader    io.Reader
	ctx           context.Context
}

func NewCloseReader(ctx context.Context, mainReader io.Reader, readerToClose io.ReadCloser) *CloseReader {
	return &CloseReader{
		mainReader:    mainReader,
		readerToClose: readerToClose,
		ctx:           ctx,
	}
}

func (c *CloseReader) Read(p []byte) (n int, err error) {
	n, err = c.mainReader.Read(p)
	if err != nil {
		// during streaming, it's impossible to close resp.Body as usual via defer resp.Body.Close()
		// so that's necessary to delegate it
		closeErr := c.readerToClose.Close()
		if closeErr != nil {
			zerolog.Ctx(c.ctx).Error().Err(closeErr).Msg("couldn't close a body reader during streaming")
		}
	}

	return n, err
}
