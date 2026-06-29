package utils

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

func CloseBodyReader(ctx context.Context, bodyReader io.ReadCloser) {
	err := bodyReader.Close()
	if err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("couldn't close a body reader")
	}
}

func DefaultDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 15 * time.Second,
	}
}

func DefaultHttpTransport() *http.Transport {
	return &http.Transport{
		DialContext:           DefaultDialer().DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   512,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	}
}
