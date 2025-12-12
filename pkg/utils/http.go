package utils

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

func CloseBodyReader(ctx context.Context, bodyReader io.ReadCloser) {
	err := bodyReader.Close()
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("couldn't close a body reader")
	}
}

func DefaultHttpTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   256,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	}
}
