package stats

import (
	"context"
	"testing"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/stretchr/testify/assert"
)

func TestCreateStatsService(t *testing.T) {
	tests := []struct {
		name      string
		config    *config.StatsConfig
		condition func(actual StatsService) bool
	}{
		{
			name: "noop if config is nil",
			condition: func(actual StatsService) bool {
				_, ok := actual.(*noopStatsService)
				return ok
			},
		},
		{
			name:   "noop if disabled",
			config: &config.StatsConfig{Enabled: false},
			condition: func(actual StatsService) bool {
				_, ok := actual.(*noopStatsService)
				return ok
			},
		},
		{
			name:   "base stats service",
			config: &config.StatsConfig{Enabled: true, Type: config.Local},
			condition: func(actual StatsService) bool {
				_, ok := actual.(*BaseStatsService)
				return ok
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			service := NewStatsService(context.Background(), test.config, integration.NewIntegrationResolver(nil))

			assert.True(te, test.condition(service))
		})
	}
}
