package upstreams

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// typedStubConnector answers GetType() - the head processor and the cap detectors
// call it during construction - and nothing else, since the upstream is never started.
type typedStubConnector struct {
	connectors.ApiConnector
}

func (t *typedStubConnector) GetType() specs.ApiConnectorType {
	return specs.JsonRpcConnector
}

func TestNewGenericUpstreamSeedsManualLabels(t *testing.T) {
	upstream := newUpstreamWithLabelsDetection(t, config.UpstreamLabels{
		"archive":  "false",
		"provider": "hetzner",
	}, false)

	assert.Equal(t, map[string]string{
		"archive":  "false",
		"provider": "hetzner",
	}, upstream.GetUpstreamState().Labels.GetAllLabels())
}

func TestNewGenericUpstreamWithoutManualLabelsHasEmptyLabels(t *testing.T) {
	upstream := newUpstreamWithLabelsDetection(t, nil, false)

	assert.Empty(t, upstream.GetUpstreamState().Labels.GetAllLabels())
}

// TestNewGenericUpstreamSeedsManualLabelsEvenWhenLabelsDetectionDisabled covers the
// headline promise in the docs: manual labels are seeded into upstream state at
// construction time, independent of whether the runtime label detectors are ever
// started. Every other seeding test here runs with detection enabled, so on its own
// none of them proves the seed survives disable-labels-detection: true.
func TestNewGenericUpstreamSeedsManualLabelsEvenWhenLabelsDetectionDisabled(t *testing.T) {
	upstream := newUpstreamWithLabelsDetection(t, config.UpstreamLabels{
		"archive":  "false",
		"provider": "hetzner",
	}, true)

	assert.Equal(t, map[string]string{
		"archive":  "false",
		"provider": "hetzner",
	}, upstream.GetUpstreamState().Labels.GetAllLabels())
}

func TestDetectorLabelOverwritesManualLabel(t *testing.T) {
	upstream := newUpstreamWithLabelsDetection(t, config.UpstreamLabels{"archive": "true"}, false)

	seededValue, ok := upstream.GetUpstreamState().Labels.GetLabel("archive")
	require.True(t, ok)
	assert.Equal(t, "true", seededValue, "guard: the manual seed must be in place before the detector event is applied")

	event := &protocol.LabelsUpstreamStateEvent{Labels: lo.T2("archive", "false")}
	nextState := event.ProcessEvent(upstream.GetUpstreamState())

	value, ok := nextState.Labels.GetLabel("archive")
	require.True(t, ok)
	assert.Equal(t, "false", value, "a detector event must win over the manual seed")
}

func newUpstreamWithLabelsDetection(t *testing.T, labels config.UpstreamLabels, disableLabelsDetection bool) *GenericUpstream {
	t.Helper()
	require.NoError(t, specs.NewMethodSpecLoader().Load())

	disabled, enabled := false, true
	conf := &config.Upstream{
		Id:            "u1",
		ChainName:     "ethereum",
		HeadConnector: "json-rpc",
		PollInterval:  time.Second,
		Methods:       &config.MethodsConfig{BanDuration: time.Minute},
		Labels:        labels,
		Options: &chains.Options{
			InternalTimeout:             time.Second,
			ValidationInterval:          time.Second,
			DisableValidation:           &disabled,
			DisableChainValidation:      &enabled,
			DisableSettingsValidation:   &enabled,
			DisableHealthValidation:     &disabled,
			DisableLowerBoundsDetection: &enabled,
			DisableLabelsDetection:      &disableLabelsDetection,
			ValidateSyncing:             &disabled,
			ValidatePeers:               &disabled,
			ValidateCallLimit:           &disabled,
			ValidateClientVersion:       &disabled,
		},
	}

	configuredChain := chains.GetChain("ethereum")
	upstreamMethods, err := methods.NewUpstreamMethods(configuredChain.MethodSpec, conf.Methods, nil)
	require.NoError(t, err)

	stub := &typedStubConnector{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	upstream, err := NewGenericUpstream(ctx, cancel, conf, configuredChain, 12, &upstreamCreationData{
		upstreamConnectorsInfo: &connectorsInfo{
			internalRequestConnector: stub,
			headConnector:            stub,
		},
		upstreamMethods: upstreamMethods,
	})
	require.NoError(t, err)
	return upstream
}
