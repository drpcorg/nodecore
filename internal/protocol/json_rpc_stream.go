package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/rs/zerolog"
	"io"
)

const MaxChunkSize = 8192

func ProcessFirstChunk(reader *bufio.Reader, chunkSize int) bool {
	// analyze the first chunk to determine if there is an error or not
	// if there is an error then it's unnecessary to stream such responses
	body, err := reader.Peek(chunkSize)
	if err != nil && err != io.EOF {
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
			zerolog.Ctx(c.ctx).Warn().Err(closeErr).Msg("couldn't close a body reader during streaming")
		}
	}

	return n, err
}
