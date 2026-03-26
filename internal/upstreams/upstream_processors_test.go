package upstreams_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func TestCreateHeadEventProcessor_ReturnsHeadProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:           "upstream-id",
		ChainName:    chains.POLYGON.String(),
		PollInterval: 100 * time.Millisecond,
		Options:      testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(conf.ChainName), conf.Options)

	processor := upstreams.CreateHeadEventProcessor(context.Background(), conf, connector, chainSpecific)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.HeadEventProcessor{}, processor)
	assert.Equal(t, event_processors.HeadEventProcessorType, processor.Type())
}

func TestCreateHealthEventProcessor_ReturnsNilWithoutValidators(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateHealthEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateHealthEventProcessor_ReturnsNilWhenValidationDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableValidation(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateHealthEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateHealthEventProcessor_ReturnsNilWhenHealthValidationDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableHealthValidation(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateHealthEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateHealthEventProcessor_ReturnsHealthProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateHealthEventProcessor(context.Background(), conf, chainSpecific)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.BaseHealthEventProcessor{}, processor)
	assert.Equal(t, event_processors.HealthValidatorProcessorType, processor.Type())
}

func TestCreateSettingsEventProcessor_ReturnsNilWhenValidatorsDisabledByChainSpecific(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableChainValidation(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateSettingsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateSettingsEventProcessor_ReturnsNilOnlyWhenSettingsValidationDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableSettingsValidation(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateSettingsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateSettingsEventProcessor_ReturnsNilWhenOnlyGlobalValidationDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableValidation(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateSettingsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateSettingsEventProcessor_ReturnsSettingsProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateSettingsEventProcessor(context.Background(), conf, chainSpecific)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.BaseSettingsEventProcessor{}, processor)
	assert.Equal(t, event_processors.SettingsValidatorProcessorType, processor.Type())
}

func TestCreateLowerBoundsEventProcessor_ReturnsNilWithoutProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateLowerBoundsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateLowerBoundsEventProcessor_ReturnsNilWhenDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableLowerBoundsDetection(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateLowerBoundsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateLowerBoundsEventProcessor_ReturnsProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateLowerBoundsEventProcessor(context.Background(), conf, chainSpecific)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.BaseLowerBoundEventProcessor{}, processor)
	assert.Equal(t, event_processors.LowerBoundEventProcessorType, processor.Type())
}

func TestCreateLabelsEventProcessor_ReturnsNilWithoutProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateLabelsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateLabelsEventProcessor_ReturnsNilWhenDisabled(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(withDisableLabelsDetection(true)),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateLabelsEventProcessor(context.Background(), conf, chainSpecific)

	assert.Nil(t, processor)
}

func TestCreateLabelsEventProcessor_ReturnsProcessor(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateLabelsEventProcessor(context.Background(), conf, chainSpecific)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.LabelsEventProcessor{}, processor)
	assert.Equal(t, event_processors.LabelsProcessorType, processor.Type())
}

func TestCreateBlockEventProcessor_ReturnsNilForUnsupportedBlockchain(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewSolanaChainSpecificObject(context.Background(), chains.GetChain(chains.SOLANA.String()), conf.Id, connector, conf.Options.InternalTimeout, conf.Options.ValidationInterval)

	processor := upstreams.CreateBlockEventProcessor(context.Background(), conf, connector, chainSpecific, chains.Solana)

	assert.Nil(t, processor)
}

func TestCreateBlockEventProcessor_ReturnsProcessorForEthereum(t *testing.T) {
	conf := &config.Upstream{
		Id:      "upstream-id",
		Options: testUpstreamOptions(),
	}
	connector := mocks.NewConnectorMock()
	chainSpecific := specific.NewEvmChainSpecific(conf.Id, connector, chains.GetChain(chains.POLYGON.String()), conf.Options)

	processor := upstreams.CreateBlockEventProcessor(context.Background(), conf, connector, chainSpecific, chains.Ethereum)

	assert.NotNil(t, processor)
	assert.IsType(t, &event_processors.BaseBlockEventProcessor{}, processor)
	assert.Equal(t, event_processors.BlockEventProcessorType, processor.Type())
}

type upstreamOptionsMutator func(*config.UpstreamOptions)

func testUpstreamOptions(mutators ...upstreamOptionsMutator) *config.UpstreamOptions {
	options := &config.UpstreamOptions{
		InternalTimeout:             time.Second,
		ValidationInterval:          time.Second,
		DisableValidation:           new(false),
		DisableSettingsValidation:   new(false),
		DisableChainValidation:      new(false),
		DisableHealthValidation:     new(false),
		DisableLowerBoundsDetection: new(false),
		DisableLabelsDetection:      new(false),
	}

	for _, mutate := range mutators {
		mutate(options)
	}

	return options
}

func withDisableValidation(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableValidation = new(v)
	}
}

func withDisableSettingsValidation(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableSettingsValidation = new(v)
	}
}

func withDisableChainValidation(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableChainValidation = new(v)
	}
}

func withDisableHealthValidation(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableHealthValidation = new(v)
	}
}

func withDisableLowerBoundsDetection(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableLowerBoundsDetection = new(v)
	}
}

func withDisableLabelsDetection(v bool) upstreamOptionsMutator {
	return func(options *config.UpstreamOptions) {
		options.DisableLabelsDetection = new(v)
	}
}
