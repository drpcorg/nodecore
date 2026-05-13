package connectors

import (
	"context"

	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type ApiConnector interface {
	utils.Lifecycle

	SendRequest(context.Context, protocol.RequestHolder) protocol.ResponseHolder
	Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error)
	Unsubscribe(opId string)
	GetType() specs.ApiConnectorType

	SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState]
}
