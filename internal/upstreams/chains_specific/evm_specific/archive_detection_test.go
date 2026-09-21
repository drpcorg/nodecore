package evm_specific

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/labels/eth_labels"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

func TestArchiveDetectionSuppressed(t *testing.T) {
	tests := []struct {
		name         string
		manualLabels map[string]string
		suppressed   bool
	}{
		{name: "no labels", manualLabels: nil, suppressed: false},
		{name: "unrelated label", manualLabels: map[string]string{"provider": "hetzner"}, suppressed: false},
		{name: "archive false", manualLabels: map[string]string{"archive": "false"}, suppressed: true},
		{name: "archive true", manualLabels: map[string]string{"archive": "true"}, suppressed: false},
		{name: "archive capitalised", manualLabels: map[string]string{"archive": "False"}, suppressed: false},
		{name: "archive empty", manualLabels: map[string]string{"archive": ""}, suppressed: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			assert.Equal(te, test.suppressed, archiveDetectionSuppressed(test.manualLabels))
		})
	}
}

func TestNewEvmChainSpecificKeepsManualLabels(t *testing.T) {
	chainSpecific := NewEvmChainSpecific(
		context.Background(),
		"u1",
		nil,
		nil,
		chains.GetChain(chains.ETHEREUM.String()),
		time.Second,
		&chains.Options{InternalTimeout: time.Second, ValidationInterval: time.Second},
		map[string]string{"archive": "false"},
	)

	assert.True(t, archiveDetectionSuppressed(chainSpecific.manualLabels),
		"manual labels must reach the chain-specific object so the archive probe is skipped")
}

// TestLabelsDetectorsArchiveDetector checks the detector list LabelsProcessor is built
// from, not just the archiveDetectionSuppressed predicate in isolation: a manual
// "archive": "false" label must keep the archive detector out of the list entirely.
func TestLabelsDetectorsArchiveDetector(t *testing.T) {
	tests := []struct {
		name          string
		manualLabels  map[string]string
		wantsDetector bool
	}{
		{name: "no manual labels", manualLabels: nil, wantsDetector: true},
		{name: "archive false", manualLabels: map[string]string{"archive": "false"}, wantsDetector: false},
		{name: "archive true", manualLabels: map[string]string{"archive": "true"}, wantsDetector: true},
		{name: "archive capitalised", manualLabels: map[string]string{"archive": "False"}, wantsDetector: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(te *testing.T) {
			chainSpecific := NewEvmChainSpecific(
				context.Background(),
				"u1",
				nil,
				nil,
				chains.GetChain(chains.ETHEREUM.String()),
				time.Second,
				&chains.Options{InternalTimeout: time.Second, ValidationInterval: time.Second},
				test.manualLabels,
			)

			assert.Equal(te, test.wantsDetector, hasArchiveDetector(chainSpecific.labelsDetectors()))
		})
	}
}

func hasArchiveDetector(detectors []labels.LabelsDetector) bool {
	for _, detector := range detectors {
		if _, ok := detector.(*eth_labels.EthArchiveLabelsDetector); ok {
			return true
		}
	}
	return false
}
