package emerald

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/drpcorg/nodecore/internal/protocol"
)

const (
	// streamReadChunkSize is the buffer size for continuation reads from the
	// upstream body once streaming is underway. It is decoupled from
	// protocol.MaxChunkSize (8KB), which only gates the first-chunk envelope
	// analysis: larger reads here mean fewer, bigger gRPC frames and fewer trips
	// through the read/send loop for a multi-MB body.
	streamReadChunkSize = 32 * 1024
	// streamReadAheadDepth bounds how many read-ahead buffers may be in flight
	// (≈ depth * streamReadChunkSize bytes per stream). It lets the producer keep
	// draining the upstream while the consumer is blocked in a cross-network
	// stream.Send.
	streamReadAheadDepth = 4
)

// chunkSink receives the extracted result bytes one chunk at a time. final is
// true on the chunk that completes the result value, so the consumer can mark
// end-of-stream inline instead of via a trailing empty frame.
type chunkSink interface {
	WriteChunk(p []byte, final bool) error
}

// readChunk carries one read-ahead buffer (or a read error) from the producer
// goroutine to the consumer.
type readChunk struct {
	buf []byte
	err error
}

// chunkProcessor consumes one read chunk and reports whether the logical stream
// is complete (done) so the read-ahead loop can stop emitting and just drain.
type chunkProcessor func(buf []byte) (done bool, err error)

// streamReadAhead reads from reader in a background goroutine and feeds chunks,
// in order, to process on the calling goroutine - so an upstream read overlaps
// the (cross-network) stream.Send that process performs. It always drains reader
// to EOF, even after process reports done, so the protocol.CloseReader wrapping
// resp.Body observes EOF and closes the body.
//
// Each chunk is a freshly allocated buffer, so the producer never overwrites a
// buffer still queued in the channel or still being marshaled by process. This
// matches the chunk emitter's aliasing assumption: stream.Send marshals
// synchronously, so a buffer is free the moment process returns.
func streamReadAhead(ctx context.Context, reader io.Reader, process chunkProcessor) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // unblocks a producer parked on the channel send if we return early

	// On early return (process error), a producer parked in reader.Read would
	// otherwise linger until the upstream/HTTP timeout releases it, since the
	// cancel above only covers the channel send. Closing the reader unblocks
	// that Read deterministically (CloseReader.Close is idempotent, so the
	// producer's own read-error close is harmless). Readers that aren't Closers
	// (tests) are left alone.
	if closer, ok := reader.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	ch := make(chan readChunk, streamReadAheadDepth)
	go func() {
		defer close(ch)
		for {
			buf := make([]byte, streamReadChunkSize)
			n, err := reader.Read(buf)
			if n > 0 {
				select {
				case ch <- readChunk{buf: buf[:n]}:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					select {
					case ch <- readChunk{err: err}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()

	done := false
	for rc := range ch {
		if done {
			// The logical stream is already complete: keep draining so the
			// producer reaches EOF and CloseReader closes resp.Body, but ignore
			// read errors (e.g. cancellation or an upstream reset during the
			// envelope tail). The full value was already emitted, so surfacing a
			// late error here would send a spurious error frame after the final
			// chunk. This mirrors the old io.Copy(io.Discard, reader) drain that
			// dropped its error.
			continue
		}
		if rc.err != nil {
			return rc.err
		}
		d, err := process(rc.buf)
		if err != nil {
			return err
		}
		done = d
	}
	return nil
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
func streamJsonRPCResult(ctx context.Context, reader io.Reader, sink chunkSink, start int, counter protocol.ResultCounter) error {
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

	if done {
		// The result value fit inside the first chunk. Drain whatever envelope
		// tail remains so the protocol.CloseReader wrapping the upstream body
		// observes EOF and closes resp.Body. The tail is small (a few bytes of
		// trailing keys plus '}').
		_, _ = io.Copy(io.Discard, reader)
		return nil
	}
	if exhausted {
		return errors.New("unable to parse stream response: result value truncated")
	}

	// Continuation: a read-ahead goroutine reads the rest of the upstream body
	// while sink.WriteChunk (-> stream.Send across the network) runs here, so the
	// two network legs overlap. streamReadAhead drains to EOF, so resp.Body is
	// closed by CloseReader without an extra io.Copy here.
	completed := false
	if err := streamReadAhead(ctx, reader, func(buf []byte) (bool, error) {
		d, werr := emitFromBuffer(sink, buf, &counter)
		if d {
			completed = true
		}
		return d, werr
	}); err != nil {
		return fmt.Errorf("unable to parse stream response: %w", err)
	}
	if !completed {
		return errors.New("unable to parse stream response: result value truncated")
	}
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
