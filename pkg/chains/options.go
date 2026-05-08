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
	DisableLabelsDetection      *bool         `yaml:"disable-labels-detection"`
	ValidateSyncing             *bool         `yaml:"validate-syncing"`
	ValidatePeers               *bool         `yaml:"validate-peers"`
	MinPeers                    int64         `yaml:"min-peers"`
	ValidateCallLimit           *bool         `yaml:"validate-call-limit"`
	CallLimitSize               int64         `yaml:"call-limit-size"`
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
