package upstreams

import (
	"context"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/blocks"
	"github.com/drpcorg/nodecore/internal/upstreams/caps"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/internal/upstreams/event_processors"
	"github.com/drpcorg/nodecore/internal/upstreams/methods"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/samber/lo"
)

func CreateHeadEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	headConnector connectors.ApiConnector,
	chainSpecific chains_specific.ChainSpecific,
	chain chains.Chain,
) event_processors.UpstreamStateEventProcessor {
	headProcessor := blocks.NewBaseHeadProcessor(ctx, conf, headConnector, chainSpecific)
	eventProcessor := event_processors.NewHeadEventProcessor(ctx, conf.Id, chain, headProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateHealthEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific chains_specific.ChainSpecific,
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
	chainSpecific chains_specific.ChainSpecific,
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
	chainSpecific chains_specific.ChainSpecific,
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
	chainSpecific chains_specific.ChainSpecific,
	configuredChain *chains.ConfiguredChain,
) event_processors.UpstreamStateEventProcessor {
	blockProcessor := createBlockProcessor(chainSpecific)
	if blockProcessor == nil {
		return nil
	}
	eventProcessor := event_processors.NewBaseBlockEventProcessor(ctx, conf.Id, configuredChain.Chain, blockProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateCapEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific chains_specific.ChainSpecific,
	connectorsInfo *connectorsInfo,
	upstreamMethods methods.Methods,
) event_processors.UpstreamStateEventProcessor {
	wsConnector, _ := lo.Find(connectorsInfo.allConnectors, func(c connectors.ApiConnector) bool {
		return c.GetType() == specs.WebsocketConnector
	})
	input := caps.DetectorInput{
		WsConnector:   wsConnector,
		HeadConnector: connectorsInfo.headConnector,
		Methods:       upstreamMethods,
	}

	capProcessor := caps.NewBaseCapProcessor(ctx, conf.Id, chainSpecific.CapDetectors(input))
	if capProcessor == nil {
		return nil
	}
	eventProcessor := event_processors.NewBaseCapEventProcessor(ctx, conf.Id, capProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}

func CreateLabelsEventProcessor(
	ctx context.Context,
	conf *config.Upstream,
	chainSpecific chains_specific.ChainSpecific,
) event_processors.UpstreamStateEventProcessor {
	labelsProcessor := createLabelsProcessor(chainSpecific, conf.Options)
	if labelsProcessor == nil {
		return nil
	}
	eventProcessor := event_processors.NewLabelsEventProcessor(ctx, conf.Id, labelsProcessor)
	if eventProcessor == nil {
		return nil
	}
	return eventProcessor
}
