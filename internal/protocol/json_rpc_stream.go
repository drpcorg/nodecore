package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/rs/zerolog"
)

const MaxChunkSize = 8192

func ResponseCanBeStreamed(reader *bufio.Reader, chunkSize int) bool {
	// analyze the first chunk to determine if there is an error or not
	// if there is an error then it's unnecessary to stream such responses
	body, err := reader.Peek(chunkSize)
	if err != nil {
		// io.EOF means the whole response is already buffered in the first chunk
		// (fewer than chunkSize bytes total), so there is nothing to stream - let
		// the buffered path parse/validate/cache it; any other error also means
		// we shouldn't stream
		return false
	}
	jsonDecoder := json.NewDecoder(bytes.NewReader(body))
	for {
		token, err := jsonDecoder.Token()
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return false
		}
		switch t := token.(type) {
		case string:
			if t == "error" {
				return false
			}
		}
	}

	return true
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

// ErrNoResultField is returned by FindResultStart when the envelope has no
// "result" key (either it ends, or it has an "error" before result).
var ErrNoResultField = errors.New("result field is missing")

// FindResultStart inspects buf — the first chunk of a JSON-RPC response —
// and locates the byte offset where the "result" value begins. It returns
// an initialized ResultCounter primed for that value's JSON type (object /
// array / string / scalar). The caller then byte-counts buf[start:] (and
// any subsequent bytes from the underlying reader) by calling Step on each
// byte until Step signals completion.
//
// Implementation:
//   - json.Decoder is used only to walk envelope keys; it stops as soon as
//     "result" or "error" is encountered.
//   - The value itself is NOT parsed — its first byte is enough to classify
//     the JSON type and initialize the counter.
func FindResultStart(buf []byte) (start int, counter ResultCounter, err error) {
	dec := json.NewDecoder(bytes.NewReader(buf))
	tok, err := dec.Token()
	if err != nil {
		return 0, ResultCounter{}, fmt.Errorf("unable to parse stream response: %w", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return 0, ResultCounter{}, errors.New("unable to parse stream response: expected json object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, ResultCounter{}, fmt.Errorf("unable to parse stream response: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return 0, ResultCounter{}, errors.New("unable to parse stream response: invalid json key")
		}
		if key == "error" {
			return 0, ResultCounter{}, ErrNoResultField
		}
		if key != "result" {
			if err := skipNextValue(dec); err != nil {
				return 0, ResultCounter{}, fmt.Errorf("unable to parse stream response: %w", err)
			}
			continue
		}
		// dec.InputOffset() points just past the closing quote of the
		// "result" key string. Advance past whitespace, the ':' separator,
		// and any following whitespace to find the first byte of the value.
		p := int(dec.InputOffset())
		for p < len(buf) && isJSONWhitespace(buf[p]) {
			p++
		}
		if p >= len(buf) || buf[p] != ':' {
			return 0, ResultCounter{}, errors.New("unable to parse stream response: missing ':' after result key")
		}
		p++
		for p < len(buf) && isJSONWhitespace(buf[p]) {
			p++
		}
		if p >= len(buf) {
			return 0, ResultCounter{}, errors.New("unable to parse stream response: truncated result value")
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
		return p, counter, nil
	}
	return 0, ResultCounter{}, ErrNoResultField
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
