package logger

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	var enableJsonLogs bool
	if logFormat := os.Getenv("LOG_FORMAT"); logFormat != "" {
		switch logFormat {
		case "json":
			enableJsonLogs = true
		case "console":
		default:
			log.Warn().Msgf("invalid log format '%s', setting 'console' format", logFormat)
		}
	}


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
