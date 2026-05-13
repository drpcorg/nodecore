package connectors

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type ApiConnector interface {
	utils.Lifecycle

	SendRequest(context.Context, protocol.RequestHolder) protocol.ResponseHolder
	Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error)
	Unsubscribe(opId string)
	GetType() chains.ApiConnectorType

	SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState]
}
