package specific

import (
	"context"
	"github.com/drpcorg/dshaltie/internal/protocol"
	"github.com/drpcorg/dshaltie/internal/upstreams/connectors"
)

type ChainSpecific interface {
	GetLatestBlock(context.Context, connectors.ApiConnector) (*protocol.Block, error)
	ParseBlock([]byte) (*protocol.Block, error)
	SubscribeHeadRequest() (protocol.UpstreamRequest, error)
	ParseSubscriptionBlock([]byte) (*protocol.Block, error)
}
