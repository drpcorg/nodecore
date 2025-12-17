package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/internal/protocol"
)

const (
	XNodecoreToken = "X-Nodecore-Token"
	XNodecoreKey   = "X-Nodecore-Key"
)

func NewAuthProcessor(ctx context.Context, authCfg *config.AuthConfig, integrationResolver *integration.IntegrationResolver) (AuthProcessor, error) {
	if authCfg == nil || !authCfg.Enabled {
		return newNoopAuthProcessor(), nil
	}
	authRequestStrategy, err := NewAuthRequestStrategy(authCfg)
	if err != nil {
		return nil, err
	}

	var authProcessor AuthProcessor
	if len(authCfg.KeyConfigs) == 0 {
		authProcessor = newSimpleAuthProcessor(authRequestStrategy)
	} else {
		keyResolver, err := NewKeyResolver(ctx, authCfg.KeyConfigs, integrationResolver)
		if err != nil {
			return nil, err
		}
		authProcessor = newBasicAuthProcessor(keyResolver, authRequestStrategy)
	}

	return authProcessor, nil
}

type AuthProcessor interface {
	Authenticate(ctx context.Context, payload AuthPayload) error
	PreKeyValidate(ctx context.Context, payload AuthPayload) ([]string, error)
	PostKeyValidate(ctx context.Context, payload AuthPayload, request protocol.RequestHolder) error
}

type AuthPayload interface {
	payload()
}

type HttpAuthPayload struct {
	httpRequest *http.Request
}

func NewHttpAuthPayload(httpRequest *http.Request) *HttpAuthPayload {
	return &HttpAuthPayload{
		httpRequest: httpRequest,
	}
}

func (h *HttpAuthPayload) payload() {}

type basicAuthProcessor struct {
	requestStrategy AuthRequestStrategy
	keyResolver     *KeyResolver
}

func (b *basicAuthProcessor) Authenticate(ctx context.Context, payload AuthPayload) error {
	return b.requestStrategy.AuthenticateRequest(ctx, payload)
}

func (b *basicAuthProcessor) PreKeyValidate(ctx context.Context, payload AuthPayload) ([]string, error) {
	key, err := b.getKey(payload)
	if err != nil {
		return nil, err
	}
	return key.PreCheckSetting(ctx)
}

func (b *basicAuthProcessor) PostKeyValidate(ctx context.Context, payload AuthPayload, request protocol.RequestHolder) error {
	key, err := b.getKey(payload)
	if err != nil {
		return err
	}
	return key.PostCheckSetting(ctx, request)
}

func (b *basicAuthProcessor) getKey(payload AuthPayload) (keymanagement.Key, error) {
	keyStr := getPayloadKey(payload)
	if keyStr == "" {
		return nil, errors.New("api-key must be provided")
	}

	key, ok := b.keyResolver.GetKey(keyStr)
	if !ok {
		return nil, errors.New("specified api-key not found")
	}
	return key, nil
}

func getPayloadKey(payload AuthPayload) string {
	var keyStr string
	switch p := payload.(type) {
	case *HttpAuthPayload:
		keyStr = p.httpRequest.PathValue("key")
		if keyStr == "" {
			keyStr = p.httpRequest.Header.Get(XNodecoreKey)
		}
	}
	return keyStr
}

func newBasicAuthProcessor(keyResolver *KeyResolver, requestStrategy AuthRequestStrategy) *basicAuthProcessor {
	return &basicAuthProcessor{
		requestStrategy: requestStrategy,
		keyResolver:     keyResolver,
	}
}

var _ AuthProcessor = (*basicAuthProcessor)(nil)
