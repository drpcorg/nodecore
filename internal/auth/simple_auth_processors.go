package auth

import (
	"context"

	"github.com/drpcorg/dsheltie/internal/protocol"
)

type noopAuthProcessor struct {
}

func (n *noopAuthProcessor) PreKeyValidate(_ context.Context, _ AuthPayload) error {
	return nil
}

func (n *noopAuthProcessor) PostKeyValidate(_ context.Context, _ AuthPayload, _ protocol.RequestHolder) error {
	return nil
}

func (n *noopAuthProcessor) Authenticate(_ context.Context, _ AuthPayload) error {
	return nil
}

func newNoopAuthProcessor() *noopAuthProcessor {
	return &noopAuthProcessor{}
}

var _ AuthProcessor = (*noopAuthProcessor)(nil)

type simpleAuthProcessor struct {
	authRequestStrategy AuthRequestStrategy
}

func (s *simpleAuthProcessor) PreKeyValidate(_ context.Context, _ AuthPayload) error {
	return nil
}

func (s *simpleAuthProcessor) PostKeyValidate(_ context.Context, _ AuthPayload, _ protocol.RequestHolder) error {
	return nil
}

func (s *simpleAuthProcessor) Authenticate(ctx context.Context, payload AuthPayload) error {
	return s.authRequestStrategy.AuthenticateRequest(ctx, payload)
}

func newSimpleAuthProcessor(authRequestStrategy AuthRequestStrategy) *simpleAuthProcessor {
	return &simpleAuthProcessor{
		authRequestStrategy: authRequestStrategy,
	}
}

var _ AuthProcessor = (*simpleAuthProcessor)(nil)
