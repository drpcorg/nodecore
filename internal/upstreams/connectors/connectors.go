package connectors

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
)

type ApiConnector interface {
	SendRequest(context.Context, protocol.RequestHolder) protocol.ResponseHolder
	Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error)
	GetType() protocol.ApiConnectorType
}
