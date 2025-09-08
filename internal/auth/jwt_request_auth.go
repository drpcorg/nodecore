package auth

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

const (
	Prefix = "Bearer "
)

type jwtRequestStrategy struct {
	pubKey             any
	allowedIssuer      string
	expirationRequired bool
}

func newJwtRequestStrategy(jwtAuthCfg *config.JwtRequestStrategyConfig) (*jwtRequestStrategy, error) {
	pubKey, err := loadPubKey(jwtAuthCfg.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("unable to load public key: %s", err.Error())
	}

	return &jwtRequestStrategy{
		pubKey:             pubKey,
		allowedIssuer:      jwtAuthCfg.AllowedIssuer,
		expirationRequired: jwtAuthCfg.ExpirationRequired,
	}, nil
}

func (j *jwtRequestStrategy) AuthenticateRequest(ctx context.Context, payload AuthPayload) error {
	var token string
	switch p := payload.(type) {
	case *HttpAuthPayload:
		authHeader := p.httpRequest.Header.Get("Authorization")
		if authHeader == "" {
			return errors.New("missing Authorization header")
		}
		if !strings.HasPrefix(authHeader, Prefix) || len(authHeader) <= len(Prefix) {
			return errors.New("invalid Authorization header format")
		}
		token = strings.TrimSpace(authHeader[len(Prefix):])
	}

	parsedOptions := make([]jwt.ParserOption, 0)
	if j.expirationRequired {
		parsedOptions = append(parsedOptions, jwt.WithExpirationRequired())
	}
	if j.allowedIssuer != "" {
		parsedOptions = append(parsedOptions, jwt.WithIssuer(j.allowedIssuer))
	}

	parser := jwt.NewParser(parsedOptions...)

	_, err := parser.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return j.pubKey, nil
	})
	if err != nil {
		return fmt.Errorf("unable to parse jwt token: %s", err.Error())
	}

	return nil
}

var _ AuthRequestStrategy = (*jwtRequestStrategy)(nil)

func loadPubKey(path string) (any, error) {
	if path == "" {
		return nil, errors.New("public key path is empty")
	}
	pubKeyBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(pubKeyBytes)
	if block != nil {
		if key, ok := parsePubKey(block.Bytes); ok {
			return key, nil
		}
	} else {
		if key, ok := parsePubKey(pubKeyBytes); ok {
			return key, nil
		}
	}

	return nil, errors.New("unknown public key format")
}

func parsePubKey(pubKeyBytes []byte) (any, bool) {
	// try PKIX
	key, err := x509.ParsePKIXPublicKey(pubKeyBytes)
	if err == nil {
		return key, true
	}
	// try RSA
	key, err = x509.ParsePKCS1PublicKey(pubKeyBytes)
	if err == nil {
		return key, true
	}

	// try certificate
	cert, err := x509.ParseCertificate(pubKeyBytes)
	if err == nil && cert != nil {
		return cert.PublicKey, true
	}

	return nil, false
}
