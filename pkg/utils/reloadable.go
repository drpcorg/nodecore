package utils

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Format is how the fetched bytes of a Reloadable are decoded.
type Format int

const (
	JSON Format = iota
	YAML
)

const reloadableFetchTimeout = 10 * time.Second

// Reloadable holds a value that is refreshed from a source on a period: an http(s)
// URL fetched with a GET, or a local file path (a file:// prefix is accepted). A
// refresh decodes the bytes into a
// fresh T and swaps it in; unchanged bytes are skipped, and a fetch or decode failure
// keeps the current value. Whatever a T derives from its fields it does in its own
// UnmarshalYAML / UnmarshalJSON hook. Readers never mutate what Get returns.
type Reloadable[T any] struct {
	value  atomic.Pointer[T]
	source string
	format Format
	period time.Duration

	lastHash uint64
	failing  bool
}

// NewReloadable returns the holder seeded with initial and starts the refresh loop on
// ctx: the first refresh runs right away in the background, then one per period.
func NewReloadable[T any](ctx context.Context, initial *T, source string, format Format, period time.Duration) *Reloadable[T] {
	r := &Reloadable[T]{
		source: source,
		format: format,
		period: period,
	}
	r.value.Store(initial)
	go r.loop(ctx)
	return r
}

func (r *Reloadable[T]) Get() *T {
	return r.value.Load()
}

func (r *Reloadable[T]) loop(ctx context.Context) {
	r.refresh(ctx)

	ticker := time.NewTicker(r.period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *Reloadable[T]) refresh(ctx context.Context) {
	err := r.load(ctx)
	if err == nil {
		r.failing = false
		return
	}
	event := log.Debug()
	if !r.failing {
		event = log.Warn()
	}
	r.failing = true
	event.Err(err).Msgf("unable to reload from '%s', keeping the current value", r.source)
}

func (r *Reloadable[T]) load(ctx context.Context) error {
	data, err := fetch(ctx, r.source)
	if err != nil {
		return err
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write(data)
	hash := hasher.Sum64()
	if hash == r.lastHash {
		log.Debug().Msgf("content of '%s' is unchanged, reload skipped", r.source)
		return nil
	}

	value, err := r.decode(data)
	if err != nil {
		return err
	}
	r.lastHash = hash
	r.value.Store(value)
	log.Info().Msgf("reloaded from '%s'", r.source)
	return nil
}

func (r *Reloadable[T]) decode(data []byte) (*T, error) {
	value := new(T)
	switch r.format {
	case JSON:
		if err := sonic.Unmarshal(data, value); err != nil {
			return nil, err
		}
	case YAML:
		if err := yaml.Unmarshal(data, value); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported reloadable format")
	}
	return value, nil
}

func fetch(ctx context.Context, source string) ([]byte, error) {
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		return os.ReadFile(strings.TrimPrefix(source, "file://"))
	}

	ctx, cancel := context.WithTimeout(ctx, reloadableFetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer CloseBodyReader(ctx, response.Body)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	return io.ReadAll(response.Body)
}
