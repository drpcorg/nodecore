package upstreams

import (
	"context"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	specific "github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/pkg/chains"
)

func CreateHeadEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	headConnector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
) event_processors.UpstreamStateEventProcessor {
	headProcessor := blocks.NewBaseHeadProcessor(ctx, conf, headConnector, chainSpecific)
	eventProcessor := event_processors.NewHeadEventProcessor(ctx, conf.Id, headProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateHealthEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific specific.ChainSpecific,
) event_processors.UpstreamStateEventProcessor {
	validator := createHealthValidationProcessor(chainSpecific, conf.Options)
	if validator == nil {
		return nil
	}
	eventProcessor := event_processors.NewBaseHealthEventProcessor(ctx, conf.Id, conf.Options, validator)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateSettingsEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific specific.ChainSpecific,
) event_processors.UpstreamStateEventProcessor {
	validator := createSettingValidationProcessor(chainSpecific, conf.Options)
	if validator == nil {
		return nil
	}
	eventProcessor := event_processors.NewBaseSettingsEventProcessor(ctx, conf.Id, conf.Options, validator)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateLowerBoundsEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific specific.ChainSpecific,
) event_processors.UpstreamStateEventProcessor {
	lowerBoundProcessor := createLowerBoundsProcessor(chainSpecific, conf.Options)
	if lowerBoundProcessor == nil {
		return nil
	}
	eventProcessor := event_processors.NewBaseLowerBoundEventProcessor(ctx, conf.Id, lowerBoundProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateBlockEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	requestConnector connectors.ApiConnector,
	chainSpecific specific.ChainSpecific,
	blockchainType chains.BlockchainType,
) event_processors.UpstreamStateEventProcessor {
	blockProcessor := createBlockProcessor(ctx, conf, requestConnector, chainSpecific, blockchainType)
	eventProcessor := event_processors.NewBaseBlockEventProcessor(ctx, conf.Id, blockProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}
