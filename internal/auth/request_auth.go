package auth

import (
	"context"
	"errors"

	"github.com/drpcorg/nodecore/internal/config"
)

func NewAuthRequestStrategy(authCfg *config.AuthConfig) (AuthRequestStrategy, error) {
	var authRequestStrategy AuthRequestStrategy
	var err error
	if authCfg.RequestStrategyConfig == nil {
		authRequestStrategy = &noopAuthRequestStrategy{}
	} else {
		switch authCfg.RequestStrategyConfig.Type {
		case config.Token:
			authRequestStrategy = newTokenRequestStrategy(authCfg.RequestStrategyConfig.TokenRequestStrategyConfig)
		case config.Jwt:
			authRequestStrategy, err = newJwtRequestStrategy(authCfg.RequestStrategyConfig.JwtRequestStrategyConfig)
			if err != nil {
				return nil, err
			}
		}
	}

	return authRequestStrategy, nil
}

type AuthRequestStrategy interface {
	AuthenticateRequest(ctx context.Context, payload AuthPayload) error
}

type noopAuthRequestStrategy struct {
}

func (n noopAuthRequestStrategy) AuthenticateRequest(_ context.Context, _ AuthPayload) error {
	return nil
}

var _ AuthRequestStrategy = (*noopAuthRequestStrategy)(nil)

type tokenRequestStrategy struct {
	token string
}

func newTokenRequestStrategy(tokenAuthCfg *config.TokenRequestStrategyConfig) *tokenRequestStrategy {
	return &tokenRequestStrategy{
		token: tokenAuthCfg.Value,
	}
}

func (t *tokenRequestStrategy) AuthenticateRequest(_ context.Context, payload AuthPayload) error {
	switch p := payload.(type) {
	case *HttpAuthPayload:
		requestTokenValue := p.httpRequest.Header.Get(XNodecoreToken)
		if requestTokenValue != t.token {
			return errors.New("invalid secret token")
		}
		return nil
	}
	return errors.New("invalid payload")
}

var _ AuthRequestStrategy = (*tokenRequestStrategy)(nil)
