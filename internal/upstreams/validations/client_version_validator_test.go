package validations_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/internal/upstreams/labels"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const rethRules = `
rules:
  - client: reth
    blacklist: [v1.4.0]
`

const besuRules = `
rules:
  - client: besu
    whitelist: [v24.1.0]
`

var ethereumChain = &chains.ConfiguredChain{Chain: chains.ETHEREUM, ShortNames: []string{"ethereum"}}

// rulesHolder is a registry over the given rules that never refreshes and that holds an
// upstream out when its client cannot be probed.
func rulesHolder(t *testing.T, file string) *compatible_clients.Registry {
	t.Helper()
	var rules compatible_clients.Rules
	require.NoError(t, yaml.Unmarshal([]byte(file), &rules))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	holder := utils.NewReloadable(ctx, &rules, filepath.Join(t.TempDir(), "missing.yaml"), utils.YAML, time.Hour)
	return compatible_clients.NewRegistry(holder, false)
}

// clientDetector answers web3_clientVersion with the scripted responses, in order, and
// parses every answer to the given (version, type).
func clientDetector(t *testing.T, version, clientType string, responses ...protocol.ResponseHolder) (*labels.ClientLabelsDetectorHandler, *mocks.ConnectorMock) {
	t.Helper()
	request, err := protocol.NewInternalUpstreamJsonRpcRequest("web3_clientVersion", nil, chains.ETHEREUM)
	require.NoError(t, err)
	probe := mocks.NewClientLabelsDetectorMock()
	probe.On("NodeTypeRequest").Return(request, nil)
	probe.On("ClientVersionAndType", mock.Anything).Return(version, clientType, nil)
	connector := mocks.NewConnectorMock()
	for _, response := range responses {
		connector.On("SendRequest", mock.Anything, mock.Anything).Return(response).Once()
	}
	return labels.NewClientLabelDetectorHandler("upstream-1", connector, time.Second, probe), connector
}

func answer() protocol.ResponseHolder {
	return protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"client/version"`), protocol.JsonRpc)
}

func errorAnswer() protocol.ResponseHolder {
	return protocol.NewHttpUpstreamResponseWithError(protocol.ServerError())
}

// fakeClientDetector answers DetectClient from fixed values: the validator depends on
// the labels.ClientDetector interface, not on the handler.
type fakeClientDetector struct {
	clientType, clientVersion string
	err                       error
}

func (f fakeClientDetector) DetectLabels(context.Context) map[string]string {
	return map[string]string{"client_type": f.clientType, "client_version": f.clientVersion}
}

func (f fakeClientDetector) DetectClient(context.Context) (string, string, error) {
	return f.clientType, f.clientVersion, f.err
}

func TestClientVersionValidatorAcceptsAnyClientDetector(t *testing.T) {
	var detector labels.ClientDetector = fakeClientDetector{clientType: "reth", clientVersion: "v1.4.0-abc"}
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
}

func TestClientVersionValidatorFatalOnBlacklistedVersion(t *testing.T) {
	detector, _ := clientDetector(t, "v1.4.0-5e4e5c6", "reth", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
}

func TestClientVersionValidatorValidOnAllowedVersion(t *testing.T) {
	detector, _ := clientDetector(t, "v1.5.0-abc", "reth", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
}

func TestClientVersionValidatorFatalOnWhitelistMiss(t *testing.T) {
	detector, _ := clientDetector(t, "v24.2.0", "besu", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, besuRules))

	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
}

func TestClientVersionValidatorValidWhenNoRule(t *testing.T) {
	detector, _ := clientDetector(t, "v1.13.14", "geth", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
}

func TestClientVersionValidatorValidOnUnknownClient(t *testing.T) {
	detector, _ := clientDetector(t, "unknown", "default client", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
}

func TestClientVersionValidatorValidOnEmptyClient(t *testing.T) {
	detector, _ := clientDetector(t, "", "", answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
}

func TestClientVersionValidatorSettingsErrorOnProbeFailure(t *testing.T) {
	detector, connector := clientDetector(t, "v1.4.0", "reth", errorAnswer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, rulesHolder(t, rethRules))

	assert.Equal(t, validations.SettingsError, validator.Validate(context.Background()))
	connector.AssertNumberOfCalls(t, "SendRequest", 1)
}

func TestClientVersionValidatorValidOnProbeFailureWhenUndetectedAllowed(t *testing.T) {
	var rules compatible_clients.Rules
	require.NoError(t, yaml.Unmarshal([]byte(rethRules), &rules))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holder := utils.NewReloadable(ctx, &rules, filepath.Join(t.TempDir(), "missing.yaml"), utils.YAML, time.Hour)
	detector, connector := clientDetector(t, "v1.4.0", "reth", errorAnswer(), answer())
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, compatible_clients.NewRegistry(holder, true))

	assert.Equal(t, validations.Valid, validator.Validate(context.Background()))
	// the next tick still checks the node for real
	assert.Equal(t, validations.FatalSettingError, validator.Validate(context.Background()))
	connector.AssertNumberOfCalls(t, "SendRequest", 2)
}

func TestClientVersionValidatorSeesUpdatedRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.yaml")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holder := utils.NewReloadable(ctx, &compatible_clients.Rules{}, path, utils.YAML, 20*time.Millisecond)
	detector, connector := clientDetector(t, "v1.4.0", "reth")
	connector.On("SendRequest", mock.Anything, mock.Anything).Return(answer()) // unbounded: Eventually polls many times
	validator := validations.NewClientVersionValidator("upstream-1", ethereumChain, detector, compatible_clients.NewRegistry(holder, false))

	require.Equal(t, validations.Valid, validator.Validate(context.Background()))

	require.NoError(t, os.WriteFile(path, []byte(rethRules), 0o644))
	assert.Eventually(t, func() bool {
		return validator.Validate(context.Background()) == validations.FatalSettingError
	}, 2*time.Second, 50*time.Millisecond)
}
