package config_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestAuthInvalidRequestTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-invalid-request-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "invalid request strategy type - 'wrong-type'")
}

func TestAuthTokenNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-token-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "specified 'token' request strategy type but there are no its settings")
}

func TestAuthTokenEmptyValueThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-token-empty-value.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'token' request strategy validation, cause: there is no secret value")
}

func TestAuthJwtNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-jwt-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "specified 'jwt' request strategy type but there are no its settings")
}

func TestAuthJwtEmptyPublicKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-jwt-empty-public-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'jwt' request strategy validation, cause: there is no the public key path")
}

func TestAuthKeyNoIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-no-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, cause: no key id under index 0")
}

func TestAuthKeyDuplicateIdsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-duplicate-ids.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, key with id 'key1' already exists")
}

func TestAuthKeyDuplicateLocalKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-duplicate-local-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during key config validation, local key 'secret-1' already exists")
}

func TestAuthKeyInvalidTypeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-invalid-type.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: invalid settings strategy type - 'wrong'")
}

func TestAuthKeyLocalNoSettingsThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-local-no-settings.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: specified 'local' key management rule type but there are no its settings")
}

func TestAuthKeyLocalEmptyKeyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-key-local-empty-key.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'key1' key config validation, cause: 'key' field is empty")
}

func TestDrpcKeyNoIntegrationThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-no-drpc-integration.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'drpc-super-key' key config validation, cause: there is no drpc integration for drpc keys")
}

func TestDrpcKeyNoDrpcKeyConfigThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-no-drpc-key-config.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'drpc-super-key' key config validation, cause: specified 'drpc' key management rule type but there are no its settings")
}

func TestDrpcKeyEmptyOwnerThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-drpc-key-empty-owner.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'drpc-super-key' key config validation, cause: owner config is empty")
}

func TestDrpcKeyEmptyOwnerIdThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-drpc-key-empty-owner-id.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'drpc-super-key' key config validation, cause: owner id is empty")
}

func TestDrpcKeyEmptyOwnerTokenThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/auth/auth-drpc-key-empty-owner-token.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during 'drpc-super-key' key config validation, cause: owner API token is empty")
}
