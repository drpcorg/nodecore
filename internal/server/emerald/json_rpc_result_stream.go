package emerald

import (
	"errors"
	"fmt"
	"io"

	"github.com/drpcorg/nodecore/internal/protocol"
)

// streamJsonRPCResult extracts the bytes of the "result" field from a
// JSON-RPC response read from reader and writes them verbatim to output.
//
// The first up-to-MaxChunkSize bytes are buffered and inspected with a
// tokenizer just long enough to locate the result value's start offset and
// classify its JSON type. From that offset onward, bytes flow through
// unmodified, with a small byte-level state machine
// (protocol.ResultCounter) tracking bracket nesting / string escapes /
// scalar termination to decide when the value ends. No JSON tokens are
// allocated for the result body — it is copied byte-for-byte.
func streamJsonRPCResult(reader io.Reader, output io.Writer) error {
	prefix := make([]byte, protocol.MaxChunkSize)
	n, err := io.ReadFull(reader, prefix)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("unable to parse stream response: %w", err)
	}
	prefix = prefix[:n]
	exhausted := err != nil // io.ReadFull only stops short on EOF / UnexpectedEOF

	start, counter, err := protocol.FindResultStart(prefix)
	if err != nil {
		return err
	}

	done, err := emitFromBuffer(output, prefix[start:], &counter)
	if err != nil {
		return err
	}

	if !done {
		if exhausted {
			return errors.New("unable to parse stream response: result value truncated")
		}
		if err := emitFromReader(output, reader, &counter); err != nil {
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
// bytes to output. Returns done=true once the result value's end is
// observed.
func emitFromBuffer(output io.Writer, buf []byte, counter *protocol.ResultCounter) (bool, error) {
	flushStart := 0
	for i := 0; i < len(buf); i++ {
		switch counter.Step(buf[i]) {
		case protocol.StepContinue:
			continue
		case protocol.StepFinishHere:
			if _, err := output.Write(buf[flushStart : i+1]); err != nil {
				return false, err
			}
			return true, nil
		case protocol.StepStopBefore:
			if i > flushStart {
				if _, err := output.Write(buf[flushStart:i]); err != nil {
					return false, err
				}
			}
			return true, nil
		}
	}
	if len(buf) > flushStart {
		if _, err := output.Write(buf[flushStart:]); err != nil {
			return false, err
		}
	}
	return false, nil
}

// emitFromReader reads further chunks from reader and feeds them through
// counter until the result value's end is observed or reader is exhausted.
func emitFromReader(output io.Writer, reader io.Reader, counter *protocol.ResultCounter) error {
	chunk := make([]byte, protocol.MaxChunkSize)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			done, werr := emitFromBuffer(output, chunk[:n], counter)
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
