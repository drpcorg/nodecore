package chains

import (
	"errors"
	"time"
)

type Options struct {
	InternalTimeout             time.Duration `yaml:"internal-timeout"`
	ValidationInterval          time.Duration `yaml:"validation-interval"`
	DisableValidation           *bool         `yaml:"disable-validation"`
	DisableSettingsValidation   *bool         `yaml:"disable-settings-validation"`
	DisableChainValidation      *bool         `yaml:"disable-chain-validation"`
	DisableHealthValidation     *bool         `yaml:"disable-health-validation"`
	DisableLowerBoundsDetection *bool         `yaml:"disable-lower-bounds-detection"`
	DisableSafeBlockDetection   *bool         `yaml:"disable-safe-block-detection"`
	SupportFinalizedBlockTag    *bool         `yaml:"support-finalized-block-tag"`
	SupportSafeBlockTag         *bool         `yaml:"support-safe-block-tag"`
	DisableLabelsDetection      *bool         `yaml:"disable-labels-detection"`
	DisableLogIndexValidation   *bool         `yaml:"disable-log-index-validation"`
	ArchiveCapability           *bool         `yaml:"archive"`
	ValidateSyncing             *bool         `yaml:"validate-syncing"`
	ValidatePeers               *bool         `yaml:"validate-peers"`
	MinPeers                    int64         `yaml:"min-peers"`
	ValidateCallLimit           *bool         `yaml:"validate-call-limit"`
	ValidateClientVersion       *bool         `yaml:"validate-client-version"`
	CallLimitSize               int64         `yaml:"call-limit-size"`
}

func boolValue(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

// FinalizedBlockTagSupported returns false only when the chain explicitly
// declares support-finalized-block-tag: false. Defaults to true (supported).
func (o *Options) FinalizedBlockTagSupported() bool {
	return boolValue(o.SupportFinalizedBlockTag, true)
}

// SafeBlockTagSupported returns false only when the chain explicitly
// declares support-safe-block-tag: false. Defaults to true (supported).
func (o *Options) SafeBlockTagSupported() bool {
	return boolValue(o.SupportSafeBlockTag, true)
}

// FinalizedBlockDetectionDisabled returns true when finalized block polling
// should be skipped because the chain does not support the finalized block tag.
func (o *Options) FinalizedBlockDetectionDisabled() bool {
	if o == nil {
		return false
	}
	return !o.FinalizedBlockTagSupported()
}

// SafeBlockDetectionDisabled returns true when safe block polling should be
// skipped — either because the tag is unsupported, or because
// DisableSafeBlockDetection is explicitly set.
func (o *Options) SafeBlockDetectionDisabled() bool {
	if o == nil {
		return false
	}
	return boolValue(o.DisableSafeBlockDetection, false) || !o.SafeBlockTagSupported()
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
