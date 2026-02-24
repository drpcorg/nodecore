package config_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitShouldntHavePatternMethodEmptyThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/ratelimit-shouldnt-have-patternt-method-empty.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: error during rate limit validation, cause: the method or pattern must be specified")
}

func TestRateLimitNoNegativesThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/ratelimit-no-negatives.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error during upstream 'eth-upstream' validation, cause: error during rate limit validation, cause: the requests must be greater than 0")
}

func TestValidRateLimitBudgets(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/valid-rate-limit-budgets.yaml")
	_, err := config.NewAppConfig()
	require.NoError(t, err)
}

func TestValidRateLimitRedisEngine(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/valid-rate-limit-redis-engine.yaml")
	_, err := config.NewAppConfig()
	require.NoError(t, err)
}

func TestRateLimitDuplicateBudgetNamesThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-duplicate-budget-names.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "duplicate budget name 'duplicate-budget'")
}

func TestRateLimitBudgetEngineOverride(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-budget-engine-override.yaml")
	_, err := config.NewAppConfig()
	require.NoError(t, err)
}

func TestRateLimitBudgetNonexistentReferenceThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-budget-nonexistent-reference.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "upstream 'eth-upstream' references non-existent rate limit budget 'nonexistent-budget'")
}

func TestRateLimitAutoTuneValidConfig(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-valid.yaml")
	appConfig, err := config.NewAppConfig()
	require.NoError(t, err)
	assert.NotNil(t, appConfig)
}

func TestRateLimitAutoTunePeriodNegativeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-period-negative.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "period must be greater than 0 when auto-tune is enabled")
}

func TestRateLimitAutoTuneErrorThresholdNegativeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-error-threshold-negative.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error-threshold must be between 0 and 1")
}

func TestRateLimitAutoTuneErrorThresholdGreaterThanOneThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-error-threshold-greater-one.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "error-threshold must be between 0 and 1")
}

func TestRateLimitAutoTuneInitRateLimitNegativeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-init-rate-limit-negative.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "init-rate-limit must be greater than 0 when auto-tune is enabled")
}

func TestRateLimitAutoTuneInitRateLimitPeriodNegativeThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-init-period-negative.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "init-rate-limit-period must be greater than 0 when auto-tune is enabled")
}

func TestRateLimitAutoTuneInitRateLimitPeriodGreaterThanPeriodThenError(t *testing.T) {
	t.Setenv(config.ConfigPathVar, "configs/ratelimit/rate-limit-autotune-init-period-greater-period.yaml")
	_, err := config.NewAppConfig()
	assert.ErrorContains(t, err, "init-rate-limit-period must be less than or equal to the period when auto-tune is enabled")
}
