package logger

import (
	"context"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
	"time"
)

func init() {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	level := zerolog.InfoLevel
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		var err error
		level, err = zerolog.ParseLevel(logLevel)
		if err != nil {
			log.Warn().Err(err).Msgf("invalid log level '%s', seting 'info' level", logLevel)
		}
	}
	enableJsonLogs := false

	if enableJsonLogs {
		log.Logger = zerolog.New(os.Stdout).
			Level(level).
			With().
			Timestamp().
			Caller().
			Logger()
	} else {
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			Level(level).
			With().
			Timestamp().
			Caller().
			Int("pid", os.Getpid()).
			Logger()
	}

	zerolog.DefaultContextLogger = &log.Logger
}

func WithContext(ctx context.Context, c zerolog.Context) (context.Context, *zerolog.Logger) {
	logCopy := c.Logger()
	ctx = logCopy.WithContext(ctx)
	return ctx, &logCopy
}
