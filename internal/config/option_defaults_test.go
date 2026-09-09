package config

import (
	"testing"
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func chainSettings(options *chains.Options) chains.Settings {
	return chains.Settings{Options: options}
}

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
	}, chainSettings(&chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}), DefaultMode)

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
		DisableLogIndexValidation:   new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
		ValidateCallLimit:           new(true),
		ValidateClientVersion:       new(true),
		MinPeers:                    7,
		CallLimitSize:               7777777,
	}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			DisableValidation:           new(false),
			DisableChainValidation:      new(false),
			DisableSettingsValidation:   new(false),
			DisableHealthValidation:     new(false),
			DisableLowerBoundsDetection: new(true),
			DisableLabelsDetection:      new(true),
			DisableLogIndexValidation:   new(false),
			ValidateSyncing:             new(true),
			ValidatePeers:               new(true),
			ValidateCallLimit:           new(false),
			ValidateClientVersion:       new(false),
			MinPeers:                    3,
			CallLimitSize:               3333333,
		},
	}, chainSettings(&chains.Options{
		DisableValidation:           new(false),
		DisableChainValidation:      new(false),
		DisableSettingsValidation:   new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(true),
		DisableLabelsDetection:      new(true),
		DisableLogIndexValidation:   new(false),
		ValidateSyncing:             new(true),
		ValidatePeers:               new(true),
		ValidateCallLimit:           new(false),
		ValidateClientVersion:       new(false),
		MinPeers:                    2,
		CallLimitSize:               2222222,
	}), DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
	assert.True(t, *options.ValidateCallLimit)
	assert.Equal(t, int64(7), options.MinPeers)
	assert.Equal(t, int64(7777777), options.CallLimitSize)
}

func TestSetOptionsDefaultsUsesNodecoreDurationDefaultsBeforeChain(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			InternalTimeout:    15 * time.Second,
			ValidationInterval: time.Minute,
		},
	}, chainSettings(&chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}), DefaultMode)

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
			DisableLogIndexValidation:   new(false),
			ValidateSyncing:             new(true),
			ValidatePeers:               new(true),
			ValidateCallLimit:           new(true),
			ValidateClientVersion:       new(true),
		},
	}, chainSettings(&chains.Options{
		DisableValidation:           new(false),
		DisableChainValidation:      new(false),
		DisableSettingsValidation:   new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(true),
		DisableLabelsDetection:      new(true),
		DisableLogIndexValidation:   new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
		ValidateCallLimit:           new(false),
		ValidateClientVersion:       new(false),
	}), DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.True(t, *options.ValidateSyncing)
	assert.True(t, *options.ValidatePeers)
	assert.True(t, *options.ValidateCallLimit)
}

func TestSetOptionsDefaultsUsesNodecoreIntDefaultsBeforeChain(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			MinPeers:      5,
			CallLimitSize: 5000000,
		},
	}, chainSettings(&chains.Options{
		MinPeers:      2,
		CallLimitSize: 2000000,
	}), DefaultMode)

	assert.Equal(t, int64(5), options.MinPeers)
	assert.Equal(t, int64(5000000), options.CallLimitSize)
}

func TestSetOptionsDefaultsUsesChainDurationsWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(&chains.Options{
		InternalTimeout:    25 * time.Second,
		ValidationInterval: 90 * time.Second,
	}), DefaultMode)

	assert.Equal(t, 25*time.Second, options.InternalTimeout)
	assert.Equal(t, 90*time.Second, options.ValidationInterval)
}

func TestSetOptionsDefaultsUsesChainBoolsWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(&chains.Options{
		DisableValidation:           new(true),
		DisableChainValidation:      new(true),
		DisableSettingsValidation:   new(true),
		DisableHealthValidation:     new(true),
		DisableLowerBoundsDetection: new(false),
		DisableLabelsDetection:      new(false),
		DisableLogIndexValidation:   new(false),
		ValidateSyncing:             new(false),
		ValidatePeers:               new(false),
		ValidateCallLimit:           new(true),
		ValidateClientVersion:       new(true),
	}), DefaultMode)

	assert.True(t, *options.DisableValidation)
	assert.True(t, *options.DisableChainValidation)
	assert.True(t, *options.DisableSettingsValidation)
	assert.True(t, *options.DisableHealthValidation)
	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
	assert.True(t, *options.ValidateCallLimit)
}

func TestSetOptionsDefaultsUsesChainIntDefaultsWhenNodecoreMissing(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(&chains.Options{
		MinPeers:      4,
		CallLimitSize: 4000000,
	}), DefaultMode)

	assert.Equal(t, int64(4), options.MinPeers)
	assert.Equal(t, int64(4000000), options.CallLimitSize)
}

func TestSetOptionsDefaultsUsesHardcodedFallbacksInDefaultMode(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(nil), DefaultMode)

	assert.Equal(t, 5*time.Second, options.InternalTimeout)
	assert.Equal(t, 30*time.Second, options.ValidationInterval)
	assert.False(t, *options.DisableValidation)
	assert.False(t, *options.DisableChainValidation)
	assert.False(t, *options.DisableSettingsValidation)
	assert.False(t, *options.DisableHealthValidation)
	assert.True(t, *options.DisableLowerBoundsDetection)
	assert.True(t, *options.DisableSafeBlockDetection)
	assert.False(t, *options.DisableFinalizedBlockDetection)
	assert.True(t, *options.DisableLabelsDetection)
	assert.False(t, *options.ValidateSyncing)
	assert.False(t, *options.ValidatePeers)
	assert.False(t, *options.ValidateCallLimit)
	assert.True(t, *options.DisableLivenessSubscriptionValidation)
	assert.Equal(t, int64(1), options.MinPeers)
	assert.Equal(t, int64(1000000), options.CallLimitSize)
}

func TestSetOptionsDefaultsUsesStrictModeFallbacksForDetectionAndValidationFlags(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(nil), StrictMode)

	assert.False(t, *options.DisableLowerBoundsDetection)
	assert.False(t, *options.DisableSafeBlockDetection)
	assert.False(t, *options.DisableFinalizedBlockDetection)
	assert.False(t, *options.DisableLabelsDetection)
	assert.True(t, *options.ValidateSyncing)
	assert.True(t, *options.ValidatePeers)
	assert.True(t, *options.ValidateCallLimit)
	assert.False(t, *options.DisableLivenessSubscriptionValidation)
	assert.Equal(t, int64(1000000), options.CallLimitSize)
}

func TestSetOptionsDefaultsDisablesUnsupportedBlockTagDetection(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chains.Settings{
		SupportFinalizedBlockTag: new(false),
		SupportSafeBlockTag:      new(false),
	}, StrictMode)

	assert.True(t, *options.DisableSafeBlockDetection)
	assert.True(t, *options.DisableFinalizedBlockDetection)
}

func TestSetOptionsDefaultsHandlesNilDefaultOptions(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{}, chainSettings(&chains.Options{
		DisableValidation: new(true),
	}), DefaultMode)

	assert.True(t, *options.DisableValidation)
}

func TestSetOptionsDefaultsHandlesNilChainOptions(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{
			DisableValidation: new(true),
		},
	}, chainSettings(nil), DefaultMode)

	assert.True(t, *options.DisableValidation)
}

func TestSetOptionsDefaultsMethodsDetectionStrictModeFallback(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(nil), StrictMode)

	assert.False(t, *options.DisableMethodsDetection, "strict mode must detect methods by default")
}

func TestSetOptionsDefaultsMethodsDetectionDefaultModeFallback(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(nil), DefaultMode)

	assert.True(t, *options.DisableMethodsDetection, "default mode must not detect methods")
}

func TestSetOptionsDefaultsMethodsDetectionChainDefaultsOverrideFallback(t *testing.T) {
	options := &chains.Options{}
	chainDefaults := &ChainDefaults{Options: &chains.Options{DisableMethodsDetection: new(true)}}

	setOptionsDefaults(options, chainDefaults, chainSettings(nil), StrictMode)

	assert.True(t, *options.DisableMethodsDetection, "chain defaults must beat the strict-mode fallback")
}

func TestSetOptionsDefaultsMethodsDetectionGlobalChainSettingsOverrideFallback(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chainSettings(&chains.Options{DisableMethodsDetection: new(true)}), StrictMode)

	assert.True(t, *options.DisableMethodsDetection, "global chain settings must beat the strict-mode fallback")
}

func TestSetOptionsDefaultsMethodsDetectionUpstreamValueWins(t *testing.T) {
	options := &chains.Options{DisableMethodsDetection: new(true)}
	chainDefaults := &ChainDefaults{Options: &chains.Options{DisableMethodsDetection: new(false)}}

	setOptionsDefaults(options, chainDefaults, chainSettings(nil), StrictMode)

	assert.True(t, *options.DisableMethodsDetection, "an explicit per-upstream value must be left alone")
}

// http-response-timeout is the total budget for one upstream HTTP exchange (dial, headers AND the
// whole body). Unset inherits chain-defaults, then the chain's global settings, then 60s — the value
// that used to be hard-coded in the connector.
func TestSetOptionsDefaultsHttpResponseTimeoutFallsBackToSixtySeconds(t *testing.T) {
	options := &chains.Options{}

	setOptionsDefaults(options, nil, chains.Settings{}, DefaultMode)

	mustResolved(t, options.HttpResponseTimeout)
	assert.Equal(t, 60*time.Second, *options.HttpResponseTimeout)
}

// An explicit 0 means "no client-side timeout at all": only the caller's context ends a stuck
// exchange. It must survive defaulting even when chain defaults or global settings say otherwise.
func TestSetOptionsDefaultsHttpResponseTimeoutKeepsExplicitZero(t *testing.T) {
	options := &chains.Options{HttpResponseTimeout: new(time.Duration(0))}

	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{HttpResponseTimeout: new(30 * time.Second)},
	}, chains.Settings{Options: &chains.Options{HttpResponseTimeout: new(45 * time.Second)}}, DefaultMode)

	mustResolved(t, options.HttpResponseTimeout)
	assert.Equal(t, time.Duration(0), *options.HttpResponseTimeout)
}

func TestSetOptionsDefaultsHttpResponseTimeoutInheritsChainDefaultsOverGlobal(t *testing.T) {
	options := &chains.Options{}
	setOptionsDefaults(options, &ChainDefaults{
		Options: &chains.Options{HttpResponseTimeout: new(15 * time.Second)},
	}, chains.Settings{Options: &chains.Options{HttpResponseTimeout: new(25 * time.Second)}}, DefaultMode)
	mustResolved(t, options.HttpResponseTimeout)
	assert.Equal(t, 15*time.Second, *options.HttpResponseTimeout, "chain-defaults win over global chain settings")

	options = &chains.Options{}
	setOptionsDefaults(options, nil, chains.Settings{Options: &chains.Options{HttpResponseTimeout: new(25 * time.Second)}}, DefaultMode)
	mustResolved(t, options.HttpResponseTimeout)
	assert.Equal(t, 25*time.Second, *options.HttpResponseTimeout, "global chain settings apply when chain-defaults are silent")
}

// mustResolved fails the test when a pointer option was left unresolved by setOptionsDefaults.
func mustResolved(t *testing.T, d *time.Duration) {
	t.Helper()
	if d == nil {
		t.Fatal("expected setOptionsDefaults to resolve http-response-timeout, got nil")
	}
}
