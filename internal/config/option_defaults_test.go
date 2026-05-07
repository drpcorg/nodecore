package config

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestSetOptionsDefaultsKeepsUpstreamDurations(t *testing.T) {
	options := &chains.Options{
		InternalTimeout:    2 * time.Second,
		ValidationInterval: 45 * time.Second,
	}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			InternalTimeout:    15 * time.Second,
			ValidationInterval: time.Minute,
		},
	}, &chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}, DefaultMode)

	assert.Equal(t, 2*time.Second, options.InternalTimeout)
	assert.Equal(t, 45*time.Second, options.ValidationInterval)
}

func TestSetOptionsDefaultsKeepsUpstreamBoolAndIntValues(t *testing.T) {
	options := &chains.Options{
		DisableValidation:           new(true),
		DisableChainValidation:      new(true),
		DisableSettingsValidation:   new(true),
		DisableHealthValidation:     new(true),
		DisableLowerBoundsDetection: new(false),
		DisableLabelsDetection:      new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
		MinPeers:                    7,
	}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			DisableValidation:           new(false),
			DisableChainValidation:      new(false),
			DisableSettingsValidation:   new(false),
			DisableHealthValidation:     new(false),
			DisableLowerBoundsDetection: new(true),
			DisableLabelsDetection:      new(true),
			ValidateSyncing:             new(true),
			ValidatePeers:               new(true),
			MinPeers:                    3,
		},
	}, &chains.Options{
		DisableValidation:           new(false),
		DisableChainValidation:      new(false),
		DisableSettingsValidation:   new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(true),
		DisableLabelsDetection:      new(true),
		ValidateSyncing:             new(true),
		ValidatePeers:               new(true),
		MinPeers:                    2,
	}, DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
	assert.Equal(t, int64(7), options.MinPeers)
}

func TestSetOptionsDefaultsUsesNodecoreDurationDefaultsBeforeChain(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			InternalTimeout:    15 * time.Second,
			ValidationInterval: time.Minute,
		},
	}, &chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}, DefaultMode)

	assert.Equal(t, 15*time.Second, options.InternalTimeout)
	assert.Equal(t, time.Minute, options.ValidationInterval)
}

func TestSetOptionsDefaultsUsesNodecoreBoolDefaultsBeforeChain(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			DisableValidation:           new(true),
			DisableChainValidation:      new(true),
			DisableSettingsValidation:   new(true),
			DisableHealthValidation:     new(true),
			DisableLowerBoundsDetection: new(false),
			DisableLabelsDetection:      new(false),
			ValidateSyncing:             new(true),
			ValidatePeers:               new(true),
		},
	}, &chains.Options{
		DisableValidation:           new(false),
		DisableChainValidation:      new(false),
		DisableSettingsValidation:   new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(true),
		DisableLabelsDetection:      new(true),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
	}, DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.True(t, *options.ValidateSyncing)
	assert.True(t, *options.ValidatePeers)
}

func TestSetOptionsDefaultsUsesNodecoreMinPeersBeforeChain(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{MinPeers: 5},
	}, &chains.Options{
		MinPeers: 2,
	}, DefaultMode)

	assert.Equal(t, int64(5), options.MinPeers)
}

func TestSetOptionsDefaultsUsesChainDurationsWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, &chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}, DefaultMode)

	assert.Equal(t, 25*time.Second, options.InternalTimeout)
	assert.Equal(t, 90*time.Second, options.ValidationInterval)
}

func TestSetOptionsDefaultsUsesChainBoolsWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, &chains.Options{
		DisableValidation:           new(true),
		DisableChainValidation:      new(true),
		DisableSettingsValidation:   new(true),
		DisableHealthValidation:     new(true),
		DisableLowerBoundsDetection: new(false),
		DisableLabelsDetection:      new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
	}, DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
}

func TestSetOptionsDefaultsUsesChainMinPeersWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, &chains.Options{MinPeers: 4}, DefaultMode)

	assert.Equal(t, int64(4), options.MinPeers)
}

func TestSetOptionsDefaultsUsesHardcodedFallbacksInDefaultMode(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, nil, DefaultMode)

	assert.Equal(t, 5*time.Second, options.InternalTimeout)
	assert.Equal(t, 30*time.Second, options.ValidationInterval)
	assert.False(t, *options.DisableValidation)
	assert.False(t, *options.DisableChainValidation)
	assert.False(t, *options.DisableSettingsValidation)
	assert.False(t, *options.DisableHealthValidation)
	assert.True(t, *options.DisableLowerBoundsDetection)
	assert.True(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
	assert.Equal(t, int64(1), options.MinPeers)
}

func TestSetOptionsDefaultsUsesStrictModeFallbacksForDetectionFlags(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, nil, StrictMode)

	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.True(t, *options.ValidateSyncing)
	assert.True(t, *options.ValidatePeers)
}

func TestSetOptionsDefaultsHandlesNilDefaultOptions(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{}, &chains.Options{
		DisableValidation: new(true),
	}, DefaultMode)

	assert.True(t, *options.DisableValidation)
}

func TestSetOptionsDefaultsHandlesNilChainOptions(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			DisableValidation: new(true),
		},
	}, nil, DefaultMode)

	assert.True(t, *options.DisableValidation)
}
