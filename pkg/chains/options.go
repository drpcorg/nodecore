package chains

import (
	"errors"
	"time"
)

type Options struct {
	InternalTimeout                       time.Duration `yaml:"internal-timeout"`
	ValidationInterval                    time.Duration `yaml:"validation-interval"`
	DisableValidation                     *bool         `yaml:"disable-validation"`
	DisableSettingsValidation             *bool         `yaml:"disable-settings-validation"`
	DisableChainValidation                *bool         `yaml:"disable-chain-validation"`
	DisableHealthValidation               *bool         `yaml:"disable-health-validation"`
	DisableLowerBoundsDetection           *bool         `yaml:"disable-lower-bounds-detection"`
	DisableSafeBlockDetection             *bool         `yaml:"disable-safe-block-detection"`
	DisableFinalizedBlockDetection        *bool         `yaml:"disable-finalized-block-detection"`
	DisableLabelsDetection                *bool         `yaml:"disable-labels-detection"`
	DisableLogIndexValidation             *bool         `yaml:"disable-log-index-validation"`
	DisableLivenessSubscriptionValidation *bool         `yaml:"disable-liveness-subscription-validation"`
	ArchiveCapability                     *bool         `yaml:"archive"`
	ValidateSyncing                       *bool         `yaml:"validate-syncing"`
	ValidatePeers                         *bool         `yaml:"validate-peers"`
	MinPeers                              int64         `yaml:"min-peers"`
	ValidateCallLimit                     *bool         `yaml:"validate-call-limit"`
	ValidateClientVersion                 *bool         `yaml:"validate-client-version"`
	CallLimitSize                         int64         `yaml:"call-limit-size"`
}

func boolValue(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

// FinalizedBlockDetectionDisabled returns true when finalized block polling
// should be skipped.
func (o *Options) FinalizedBlockDetectionDisabled() bool {
	if o == nil {
		return false
	}
	return boolValue(o.DisableFinalizedBlockDetection, false)
}

// SafeBlockDetectionDisabled returns true when safe block polling should be
// skipped.
func (o *Options) SafeBlockDetectionDisabled() bool {
	if o == nil {
		return false
	}
	return boolValue(o.DisableSafeBlockDetection, false)
}

// LivenessSubscriptionValidationDisabled returns true when the ws head-liveness gating
// of WsCap should be skipped (the upstream keeps WsCap on plain ws presence).
func (o *Options) LivenessSubscriptionValidationDisabled() bool {
	if o == nil {
		return false
	}
	return boolValue(o.DisableLivenessSubscriptionValidation, false)
}

func (o *Options) Validate() error {
	if o.InternalTimeout < 0 {
		return errors.New("internal timeout can't be less than 0")
	}
	if o.ValidationInterval < 0 {
		return errors.New("validation interval can't be less than 0")
	}
	return nil
}
