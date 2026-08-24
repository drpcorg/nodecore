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

var errInvalidToken = errors.New("invalid secret token")

func (t *tokenRequestStrategy) AuthenticateRequest(_ context.Context, payload AuthPayload) error {
	switch p := payload.(type) {
	case *HttpAuthPayload:
		requestTokenValue := p.httpRequest.Header.Get(XNodecoreToken)
		if requestTokenValue != t.token {
			return errInvalidToken
		}
		return nil
	case *GrpcAuthPayload:
		requestTokenValue := ""
		if values := p.md.Get(XNodecoreToken); len(values) > 0 {
			requestTokenValue = values[0]
		}
		if requestTokenValue != t.token {
			return errInvalidToken
		}
		return nil
	}
	return errors.New("invalid payload")
}

var _ AuthRequestStrategy = (*tokenRequestStrategy)(nil)
