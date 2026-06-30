package emerald

import (
	"errors"
	"fmt"
	"io"

	"github.com/drpcorg/nodecore/internal/protocol"
)

// chunkSink receives the extracted result bytes one chunk at a time. final is
// true on the chunk that completes the result value, so the consumer can mark
// end-of-stream inline instead of via a trailing empty frame.
type chunkSink interface {
	WriteChunk(p []byte, final bool) error
}

// streamJsonRPCResult extracts the bytes of the "result" field from a
// JSON-RPC response read from reader and writes them verbatim to sink.
//
// The result value's start offset and JSON type were already located by the
// connector's single-pass AnalyzeFirstChunk and are passed in as start /
// counter, so no re-scan happens here. The first up-to-MaxChunkSize bytes are
// buffered; from start onward, bytes flow through unmodified, with a small
// byte-level state machine (protocol.ResultCounter) tracking bracket nesting /
// string escapes / scalar termination to decide when the value ends. No JSON
// tokens are allocated for the result body — it is copied byte-for-byte. The
// chunk that completes the result value is written with final=true.
func streamJsonRPCResult(reader io.Reader, sink chunkSink, start int, counter protocol.ResultCounter) error {
	prefix := make([]byte, protocol.MaxChunkSize)
	n, err := io.ReadFull(reader, prefix)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("unable to parse stream response: %w", err)
	}
	prefix = prefix[:n]
	exhausted := err != nil // io.ReadFull only stops short on EOF / UnexpectedEOF

	if start < 0 || start > len(prefix) {
		return errors.New("unable to parse stream response: result field is missing")
	}

	done, err := emitFromBuffer(sink, prefix[start:], &counter)
	if err != nil {
		return err
	}

	if !done {
		if exhausted {
			return errors.New("unable to parse stream response: result value truncated")
		}
		if err := emitFromReader(sink, reader, &counter); err != nil {
			return err
		}
	}

	// Drain whatever envelope tail remains so the protocol.CloseReader
	// wrapping the upstream body observes EOF and closes resp.Body. The
	// tail is small (a few bytes of trailing keys plus '}').
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// emitFromBuffer feeds bytes from buf into counter, writing the consumed
// bytes to sink. Returns done=true once the result value's end is observed.
// The chunk that completes the value is written with final=true; the chunk
// written when buf ends mid-value is non-final.
func emitFromBuffer(sink chunkSink, buf []byte, counter *protocol.ResultCounter) (bool, error) {
	flushStart := 0
	for i := 0; i < len(buf); i++ {
		switch counter.Step(buf[i]) {
		case protocol.StepContinue:
			continue
		case protocol.StepFinishHere:
			if err := sink.WriteChunk(buf[flushStart:i+1], true); err != nil {
				return false, err
			}
			return true, nil
		case protocol.StepStopBefore:
			if i > flushStart {
				if err := sink.WriteChunk(buf[flushStart:i], true); err != nil {
					return false, err
				}
			}
			return true, nil
		}
	}
	if len(buf) > flushStart {
		if err := sink.WriteChunk(buf[flushStart:], false); err != nil {
			return false, err
		}
	}
	return false, nil
}

// emitFromReader reads further chunks from reader and feeds them through
// counter until the result value's end is observed or reader is exhausted.
func emitFromReader(sink chunkSink, reader io.Reader, counter *protocol.ResultCounter) error {
	chunk := make([]byte, protocol.MaxChunkSize)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			done, werr := emitFromBuffer(sink, chunk[:n], counter)
			if werr != nil {
				return werr
			}
			if done {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("unable to parse stream response: result value truncated")
			}
			return fmt.Errorf("unable to parse stream response: %w", err)
		}
	}
}
