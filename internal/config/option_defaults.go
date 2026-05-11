package config

import (
	"time"

	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/samber/lo"
)

func setOptionsDefaults(
	upstreamOptions *chains.Options,
	chainDefaults *ChainDefaults,
	globalChainOptions *chains.Options,
	upstreamMode UpstreamMode,
) {
	var defaultChainOptions *chains.Options
	if chainDefaults != nil {
		defaultChainOptions = chainDefaults.Options
	}

	resolveDuration := func(defaultValue, chainValue, fallback time.Duration) time.Duration {
		if defaultValue != 0 {
			return defaultValue
		}
		if chainValue != 0 {
			return chainValue
		}
		return fallback
	}

	resolveBool := func(defaultValue, chainValue *bool, fallback bool) *bool {
		value := fallback
		if chainValue != nil {
			value = *chainValue
		}
		if defaultValue != nil {
			value = *defaultValue
		}
		return &value
	}

	resolveInt64 := func(defaultValue, chainValue, fallback int64) int64 {
		if defaultValue != 0 {
			return defaultValue
		}
		if chainValue != 0 {
			return chainValue
		}
		return fallback
	}

	getDuration := func(options *chains.Options, getter func(*chains.Options) time.Duration) time.Duration {
		if options == nil {
			return 0
		}
		return getter(options)
	}

	getBool := func(options *chains.Options, getter func(*chains.Options) *bool) *bool {
		if options == nil {
			return nil
		}
		return getter(options)
	}

	getInt64 := func(options *chains.Options, getter func(*chains.Options) int64) int64 {
		if options == nil {
			return 0
		}
		return getter(options)
	}

	if upstreamOptions.InternalTimeout == 0 {
		upstreamOptions.InternalTimeout = resolveDuration(
			getDuration(defaultChainOptions, func(options *chains.Options) time.Duration { return options.InternalTimeout }),
			getDuration(globalChainOptions, func(options *chains.Options) time.Duration { return options.InternalTimeout }),
			5*time.Second,
		)
	}
	if upstreamOptions.ValidationInterval == 0 {
		upstreamOptions.ValidationInterval = resolveDuration(
			getDuration(defaultChainOptions, func(options *chains.Options) time.Duration { return options.ValidationInterval }),
			getDuration(globalChainOptions, func(options *chains.Options) time.Duration { return options.ValidationInterval }),
			30*time.Second,
		)
	}
	if upstreamOptions.DisableValidation == nil {
		upstreamOptions.DisableValidation = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableValidation }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableValidation }),
			false,
		)
	}
	if upstreamOptions.DisableChainValidation == nil {
		upstreamOptions.DisableChainValidation = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableChainValidation }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableChainValidation }),
			false,
		)
	}
	if upstreamOptions.DisableSettingsValidation == nil {
		upstreamOptions.DisableSettingsValidation = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableSettingsValidation }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableSettingsValidation }),
			false,
		)
	}
	if upstreamOptions.DisableHealthValidation == nil {
		upstreamOptions.DisableHealthValidation = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableHealthValidation }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableHealthValidation }),
			false,
		)
	}
	if upstreamOptions.DisableLowerBoundsDetection == nil {
		upstreamOptions.DisableLowerBoundsDetection = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableLowerBoundsDetection }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableLowerBoundsDetection }),
			lo.Ternary(upstreamMode == StrictMode, false, true),
		)
	}
	if upstreamOptions.DisableLabelsDetection == nil {
		upstreamOptions.DisableLabelsDetection = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.DisableLabelsDetection }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.DisableLabelsDetection }),
			lo.Ternary(upstreamMode == StrictMode, false, true),
		)
	}
	if upstreamOptions.ValidateSyncing == nil {
		upstreamOptions.ValidateSyncing = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.ValidateSyncing }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.ValidateSyncing }),
			lo.Ternary(upstreamMode == StrictMode, true, false),
		)
	}
	if upstreamOptions.ValidatePeers == nil {
		upstreamOptions.ValidatePeers = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.ValidatePeers }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.ValidatePeers }),
			lo.Ternary(upstreamMode == StrictMode, true, false),
		)
	}
	if upstreamOptions.MinPeers == 0 {
		upstreamOptions.MinPeers = resolveInt64(
			getInt64(defaultChainOptions, func(options *chains.Options) int64 { return options.MinPeers }),
			getInt64(globalChainOptions, func(options *chains.Options) int64 { return options.MinPeers }),
			1,
		)
	}
	if upstreamOptions.ValidateCallLimit == nil {
		upstreamOptions.ValidateCallLimit = resolveBool(
			getBool(defaultChainOptions, func(options *chains.Options) *bool { return options.ValidateCallLimit }),
			getBool(globalChainOptions, func(options *chains.Options) *bool { return options.ValidateCallLimit }),
			lo.Ternary(upstreamMode == StrictMode, true, false),
		)
	}
	if upstreamOptions.CallLimitSize == 0 {
		upstreamOptions.CallLimitSize = resolveInt64(
			getInt64(defaultChainOptions, func(options *chains.Options) int64 { return options.CallLimitSize }),
			getInt64(globalChainOptions, func(options *chains.Options) int64 { return options.CallLimitSize }),
			1000000,
		)
	}
}
