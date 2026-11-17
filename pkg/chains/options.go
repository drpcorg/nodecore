package chains

import (
	"errors"
	"time"
)

// ArchiveLabel is the upstream label key that reports archive capability. It is the
// single source shared by the label detector that publishes it, the suppression check
// that honours a manually configured value, and the deprecated options.archive shim.
const ArchiveLabel = "archive"

type Options struct {
	InternalTimeout                       time.Duration `yaml:"internal-timeout,omitempty"`
	ValidationInterval                    time.Duration `yaml:"validation-interval,omitempty"`
	DisableValidation                     *bool         `yaml:"disable-validation,omitempty"`
	DisableSettingsValidation             *bool         `yaml:"disable-settings-validation,omitempty"`
	DisableChainValidation                *bool         `yaml:"disable-chain-validation,omitempty"`
	DisableHealthValidation               *bool         `yaml:"disable-health-validation,omitempty"`
	DisableLowerBoundsDetection           *bool         `yaml:"disable-lower-bounds-detection,omitempty"`
	DisableSafeBlockDetection             *bool         `yaml:"disable-safe-block-detection,omitempty"`
	DisableFinalizedBlockDetection        *bool         `yaml:"disable-finalized-block-detection,omitempty"`
	DisableLabelsDetection                *bool         `yaml:"disable-labels-detection,omitempty"`
	DisableMethodsDetection               *bool         `yaml:"disable-methods-detection,omitempty"`
	DisableLogIndexValidation             *bool         `yaml:"disable-log-index-validation,omitempty"`
	DisableErigonBugValidation            *bool         `yaml:"disable-erigon-bug-validation,omitempty"`
	DisableLivenessSubscriptionValidation *bool         `yaml:"disable-liveness-subscription-validation,omitempty"`
	// ArchiveCapability is deprecated: set the 'archive' upstream label instead. It is
	// still honoured - Upstream.setDefaults translates it into that label and warns -
	// so existing configs keep working rather than silently losing the override.
	ArchiveCapability *bool `yaml:"archive,omitempty"`
	ValidateSyncing   *bool `yaml:"validate-syncing,omitempty"`
	ValidatePeers     *bool `yaml:"validate-peers,omitempty"`
	MinPeers          int64 `yaml:"min-peers,omitempty"`
	ValidateCallLimit *bool `yaml:"validate-call-limit,omitempty"`
	CallLimitSize     int64 `yaml:"call-limit-size,omitempty"`
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
