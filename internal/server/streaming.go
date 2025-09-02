package server

import (
	"bytes"
	"context"
	"io"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	jsonArrayOpen  = []byte("[")
	jsonArrayClose = []byte("]")
	jsonArraySep   = []byte(",")
)

type SingleByteReader []byte

func (r SingleByteReader) Read(buf []byte) (int, error) {
	if len(buf) > 0 {
		buf[0] = r[0]
		return 1, io.EOF
	}
	return 0, nil
}

type DropLastReader struct {
	r io.Reader
}

func (d *DropLastReader) Read(buf []byte) (int, error) {
	w, err := d.r.Read(buf)
	if err == io.EOF {
		return max(0, w-1), err
	}
	return w, err
}

type SortingReader struct {
	ctx           context.Context
	data          <-chan *Response
	cur           io.Reader
	nextOrder     int
	buff          [][]byte
	timeoutsLimit time.Duration
}

func NewSortingReader(ctx context.Context, data <-chan *Response, expected int) io.Reader {
	return &SortingReader{
		ctx:           ctx,
		data:          data,
		buff:          make([][]byte, expected),
		timeoutsLimit: time.Second * 90,
	}
}

func (a *SortingReader) Read(buf []byte) (int, error) {
	written := 0
	for {
		// we first try to exhaust internal buffer, before reading from channel
		for {
			// if no current reader, check buffer for next one
			if a.cur == nil && len(a.buff) > a.nextOrder {
				bt := a.buff[a.nextOrder]
				if bt != nil {
					a.cur = io.MultiReader(bytes.NewReader(bt), SingleByteReader(jsonArraySep))
				}
			}
			// if current reader is not null, we need to exhaust it first
			if a.cur != nil {
				w, err := io.ReadFull(a.cur, buf[written:])
				written += w

				// any of those errors mean that current reader haven't had enough data
				// to fill buffer, so we proceed
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					// unexpected error happened
					if err != nil {
						return written, err
					} else {
						// we filled the buffer
						sbuf := make([]byte, 1)
						// we want to peek whether buffer has more data,
						// because we want to prevent situation when we returned everything and filled the buffer
						// but on next read we will return no bytes and eof, because we won't be able to cut trailing comma
						n, err := a.cur.Read(sbuf)
						if n == 0 && a.nextOrder >= len(a.buff)-1 {
							// this reader is the last one, it's exhausted and we have nothing in the buffer
							return written, err
						}

						if n == 0 {
							// this reader exhausted
							a.cur = nil
							a.nextOrder++
							return written, nil
						} else {
							a.cur = io.MultiReader(SingleByteReader(sbuf), a.cur)
							return written, nil
						}
					}
				}
				// this reader exhausted
				a.cur = nil
				a.nextOrder++
			} else {
				// we do not have any current reader and internal buffer is exhausted
				break
			}
		}
		timer := time.NewTimer(a.timeoutsLimit)
		defer timer.Stop()

		select {
		case res, ok := <-a.data:
			{
				if !ok {
					// channel won't send any updates, but we still have something in the internal buffer
					if len(a.buff) > a.nextOrder {
						// we always exauhst internal buffer first, so if we're here
						// means that there is something in internal buffer, but it's out of order
						// and we need to increase expected order to proceed
						a.nextOrder++
						continue
					}
					return written, io.EOF
				}
				// some weird stuff happened and there is a message that we don't know order for
				if res.Order < 0 {
					zerolog.Ctx(a.ctx).Error().Msgf("Unexpected no order event %d", res.Order)
					res.Order = len(a.buff)
				}
				// it's in order, then just read it immediately
				if res.Order == a.nextOrder {
					a.cur = io.MultiReader(res.ResponseReader, SingleByteReader(jsonArraySep))
				} else {
					// read into internal buffer
					bytes, err := io.ReadAll(res.ResponseReader)
					if err != nil {
						return written, err
					}
					// if this is the message without order, append to the end
					if len(a.buff) <= res.Order {
						a.buff = append(a.buff, bytes)
					} else {
						a.buff[res.Order] = bytes
					}
				}
			}
		case <-timer.C:
			{
				log.Ctx(a.ctx).Error().Msgf("unexpected timeout in sorting reader, written %v, dump: %v\n", written, a)
				if written == 0 {
					errorStr := []byte(`{"jsonrpc":"2.0","error":{"message":"couldn't perform a request"}}`)
					copy(buf, errorStr)
					return len(errorStr), io.ErrUnexpectedEOF
				}
				return written, io.EOF // same as if res,ok returned false
			}
		}
	}
}

func ArraySortingStream(ctx context.Context, data <-chan *Response, expected int) io.Reader {
	return io.MultiReader(
		SingleByteReader(jsonArrayOpen),
		&DropLastReader{NewSortingReader(ctx, data, expected)},
		SingleByteReader(jsonArrayClose))
}
