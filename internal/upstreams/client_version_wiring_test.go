package upstreams_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/evm_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/polkadot_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/compatible_clients"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/validations"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func clientVersionOnlyOptions() *chains.Options {
	return &chains.Options{
		InternalTimeout:            time.Second,
		ValidationInterval:         time.Second,
		DisableValidation:          new(false),
		DisableSettingsValidation:  new(false),
		DisableChainValidation:     new(true),
		DisableLogIndexValidation:  new(true),
		DisableErigonBugValidation: new(true),
		ValidateCallLimit:          new(false),
	}
}

func embeddedRulesHolder(t *testing.T) *compatible_clients.Registry {
	t.Helper()
	seed, err := compatible_clients.Embedded()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return compatible_clients.NewRegistry(utils.NewReloadable(ctx, seed, "", utils.YAML, time.Hour), true)
}

func evmSettingsProcessor(t *testing.T, holder *compatible_clients.Registry, clientVersion string) (event_processors.UpstreamStateEventProcessor, *mocks.ConnectorMock) {
	t.Helper()
	conf := &config.Upstream{Id: "upstream-id", Options: clientVersionOnlyOptions()}
	configuredChain := chains.GetChain("ethereum")
	connector := mocks.NewConnectorMock()
	if clientVersion != "" {
		connector.
			On("SendRequest", mock.Anything, mock.MatchedBy(func(request protocol.RequestHolder) bool {
				return request.Method() == "web3_clientVersion"
			})).
			Return(protocol.NewSimpleHttpUpstreamResponse("1", []byte(`"`+clientVersion+`"`), protocol.JsonRpc))
	}
	chainSpecific := evm_specific.NewEvmChainSpecific(context.Background(), conf.Id, connector, []connectors.ApiConnector{connector}, configuredChain, time.Second, conf.Options, nil)

	return upstreams.CreateSettingsEventProcessor(context.Background(), conf, configuredChain, chainSpecific, holder), connector
}

func TestSettingsProcessorAddsClientVersionValidatorWithRegistry(t *testing.T) {
	holder := embeddedRulesHolder(t)

	forbidden, _ := evmSettingsProcessor(t, holder, "reth/v1.4.0-5e4e5c6/x86_64-unknown-linux-gnu")
	require.NotNil(t, forbidden)
	assert.Equal(t, validations.FatalSettingError, forbidden.(*event_processors.GenericSettingsEventProcessor).Validate(context.Background()))

	allowed, _ := evmSettingsProcessor(t, holder, "Geth/v1.13.14-stable/linux-amd64/go1.21")
	require.NotNil(t, allowed)
	assert.Equal(t, validations.Valid, allowed.(*event_processors.GenericSettingsEventProcessor).Validate(context.Background()))
}

func TestSettingsProcessorSkipsClientVersionValidatorWithoutRegistry(t *testing.T) {
	processor, connector := evmSettingsProcessor(t, nil, "")

	assert.Nil(t, processor)
	connector.AssertNotCalled(t, "SendRequest", mock.Anything, mock.Anything)
}

func TestSettingsProcessorSkipsClientVersionValidatorForFamilyWithoutDetector(t *testing.T) {
	conf := &config.Upstream{Id: "upstream-id", Options: clientVersionOnlyOptions()}
	configuredChain := chains.GetChain("polkadot")
	chainSpecific := polkadot_specific.NewPolkadotChainSpecificObject(context.Background(), configuredChain, conf.Id, mocks.NewConnectorMock(), conf.Options)

	processor := upstreams.CreateSettingsEventProcessor(context.Background(), conf, configuredChain, chainSpecific, embeddedRulesHolder(t))

	assert.Nil(t, processor)
}
