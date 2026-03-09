package server

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/pkg/dshackle"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	grpcAuthVersionClaim   = "version"
	grpcAuthVersionV1      = "V1"
	grpcAuthIssuerClaim    = "iss"
	grpcAuthIssuedAtClaim  = "iat"
	grpcAuthSessionIDClaim = "sessionId"
	grpcSessionIDHeaderKey = "sessionid"

	grpcAuthTokenFreshness = 60 * time.Second
)

type grpcSessionStore struct {
	ttl      time.Duration
	sessions *utils.CMap[string, time.Time]
}

func newGrpcSessionStore(ttl time.Duration) *grpcSessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &grpcSessionStore{
		ttl:      ttl,
		sessions: utils.NewCMap[string, time.Time](),
	}
}

func (s *grpcSessionStore) Put(sessionID string) {
	if sessionID == "" {
		return
	}
	s.sessions.Store(sessionID, time.Now().Add(s.ttl))
}

func (s *grpcSessionStore) Exists(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	now := time.Now()

	expireAt, ok := s.sessions.Load(sessionID)
	if !ok {
		return false
	}
	if now.After(expireAt) {
		s.sessions.Delete(sessionID)
		return false
	}
	// dshackle behavior: extend session on access.
	s.sessions.Store(sessionID, now.Add(s.ttl))
	return true
}

type grpcSessionAuth struct {
	enabled bool
	store   *grpcSessionStore
}

func newGrpcSessionAuth(enabled bool, store *grpcSessionStore) *grpcSessionAuth {
	return &grpcSessionAuth{
		enabled: enabled,
		store:   store,
	}
}

func (a *grpcSessionAuth) requireSession(ctx context.Context) error {
	if a == nil || !a.enabled {
		return nil
	}
	if a.store == nil {
		return status.Error(codes.Unauthenticated, "Session store is not configured")
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no metadata")
	}

	sessionID := ""
	values := md.Get(grpcSessionIDHeaderKey)
	if len(values) > 0 {
		sessionID = strings.TrimSpace(values[0])
	}

	if sessionID == "" {
		return status.Error(codes.Unauthenticated, "sessionId is not passed")
	}
	if !a.store.Exists(sessionID) {
		return status.Error(codes.Unauthenticated, fmt.Sprintf("Session %s does not exist", sessionID))
	}
	return nil
}

type GrpcAuthService struct {
	dshackle.UnimplementedAuthServer

	enabled            bool
	publicKeyOwner     string
	privateKey         *rsa.PrivateKey
	externalPublicKey  *rsa.PublicKey
	sessions           *grpcSessionStore
	allowedAuthVersion string
}

func NewGrpcAuthService(grpcAuthConfig *config.GrpcAuthConfig) (*GrpcAuthService, *grpcSessionAuth, error) {
	cfg := grpcAuthConfig
	if cfg == nil {
		cfg = &config.GrpcAuthConfig{
			Enabled: false,
		}
	}

	if !cfg.Enabled {
		return nil, nil, nil
	}

	store := newGrpcSessionStore(cfg.SessionTTL)
	sessionAuth := newGrpcSessionAuth(cfg.Enabled, store)
	service := &GrpcAuthService{
		enabled:            cfg.Enabled,
		publicKeyOwner:     cfg.PublicKeyOwner,
		sessions:           store,
		allowedAuthVersion: grpcAuthVersionV1,
	}

	privateKey, err := loadRSAPrivateKeyFromFile(cfg.ProviderPrivateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to load grpc provider private key: %w", err)
	}
	externalPublicKey, err := loadRSAPublicKeyFromFile(cfg.ExternalPublicKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to load grpc external public key: %w", err)
	}

	service.privateKey = privateKey
	service.externalPublicKey = externalPublicKey

	return service, sessionAuth, nil
}

func (s *GrpcAuthService) Authenticate(_ context.Context, request *dshackle.AuthRequest) (*dshackle.AuthResponse, error) {
	if !s.enabled {
		return nil, status.Error(codes.Unimplemented, "Authentication process is not enabled")
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid request: request is nil")
	}
	if err := s.validateIncomingToken(request.GetToken()); err != nil {
		return nil, err
	}

	issuedAt := time.Now()
	sessionID := uuid.NewString()
	providerToken := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		grpcAuthIssuedAtClaim:  issuedAt.Unix(),
		grpcAuthSessionIDClaim: sessionID,
		grpcAuthVersionClaim:   s.allowedAuthVersion,
	})
	signedToken, err := providerToken.SignedString(s.privateKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error during auth - Internal error: %s", err.Error())
	}

	s.sessions.Put(sessionID)

	return &dshackle.AuthResponse{
		ProviderToken: signedToken,
	}, nil
}

func (s *GrpcAuthService) validateIncomingToken(token string) error {
	if token == "" {
		return status.Error(codes.InvalidArgument, "Invalid token: token is empty")
	}
	if s.externalPublicKey == nil {
		return status.Error(codes.Internal, "Error during auth - External public key is not configured")
	}

	parsedToken, err := jwt.Parse(
		token,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
			}
			return s.externalPublicKey, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
	)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "Invalid token: %s", err.Error())
	}
	if !parsedToken.Valid {
		return status.Error(codes.InvalidArgument, "Invalid token: token is invalid")
	}

	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		return status.Error(codes.InvalidArgument, "Invalid token: unsupported claims")
	}

	versionRaw, hasVersion := claims[grpcAuthVersionClaim]
	if !hasVersion {
		return status.Error(codes.InvalidArgument, "Version is not specified in the token")
	}
	version := fmt.Sprintf("%v", versionRaw)
	if version != s.allowedAuthVersion {
		return status.Errorf(codes.InvalidArgument, "Unsupported auth version %s", version)
	}

	issuerRaw, hasIssuer := claims[grpcAuthIssuerClaim]
	if !hasIssuer {
		return status.Error(codes.InvalidArgument, "Invalid token: issuer is not specified")
	}
	issuer, ok := issuerRaw.(string)
	if !ok || issuer != s.publicKeyOwner {
		return status.Errorf(codes.InvalidArgument, "Invalid token: invalid issuer %v", issuerRaw)
	}

	iatRaw, hasIssuedAt := claims[grpcAuthIssuedAtClaim]
	if !hasIssuedAt {
		return status.Error(codes.InvalidArgument, "Invalid token: iat is not specified")
	}
	issuedAtUnix, err := parseJWTNumericClaim(iatRaw)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "Invalid token: invalid iat (%s)", err.Error())
	}
	issuedAt := time.Unix(issuedAtUnix, 0)
	now := time.Now()
	if issuedAt.Before(now.Add(-grpcAuthTokenFreshness)) || issuedAt.After(now.Add(grpcAuthTokenFreshness)) {
		return status.Error(codes.InvalidArgument, "Invalid token: iat is out of allowed range")
	}

	return nil
}

func parseJWTNumericClaim(value any) (int64, error) {
	switch v := value.(type) {
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case json.Number:
		return v.Int64()
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func loadRSAPrivateKeyFromFile(path string) (*rsa.PrivateKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPrivateKeyFromPEM(content)
}

func loadRSAPublicKeyFromFile(path string) (*rsa.PublicKey, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return jwt.ParseRSAPublicKeyFromPEM(content)
}
