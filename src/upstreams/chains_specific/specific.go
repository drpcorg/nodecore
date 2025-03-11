package specific

import (
	"context"
	"github.com/drpcorg/dshaltie/src/protocol"
	"github.com/drpcorg/dshaltie/src/upstreams/connectors"
)

type ChainSpecific interface {
	GetLatestBlock(context.Context, connectors.ApiConnector) (*protocol.Block, error)
	ParseBlock([]byte) (*protocol.Block, error)
	LatestBlockRequest() (protocol.UpstreamRequest, error)
	SubscribeHeadRequest() (protocol.UpstreamRequest, error)
}
