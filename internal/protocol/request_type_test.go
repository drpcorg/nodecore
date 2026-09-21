package protocol_test

import (
	"testing"

	"github.com/drpcorg/nodecore/internal/protocol"
	specs "github.com/drpcorg/public/pkg/methods"
	"github.com/stretchr/testify/assert"
)

func TestRequestTypeCanBeServedBy(t *testing.T) {
	tests := []struct {
		name        string
		requestType protocol.RequestType
		connectors  []specs.ApiConnectorType
		expected    bool
	}{
		{"json-rpc over json-rpc connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.JsonRpcConnector}, true},
		{"json-rpc over websocket connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.WebsocketConnector}, true},
		{"json-rpc over tendermint connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.TendermintConnector}, true},
		{"json-rpc over rest-additional connector (translated method)", protocol.JsonRpc, []specs.ApiConnectorType{specs.RestAdditional}, true},
		{"json-rpc over grpc connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"json-rpc when a non-grpc connector is present", protocol.JsonRpc, []specs.ApiConnectorType{specs.GrpcConnector, specs.JsonRpcConnector}, true},
		{"ws over grpc connector", protocol.Ws, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"ws over websocket connector", protocol.Ws, []specs.ApiConnectorType{specs.WebsocketConnector}, true},
		{"rest over rest connector", protocol.Rest, []specs.ApiConnectorType{specs.RestConnector}, true},
		{"rest over json-rpc connector (algorand declaration)", protocol.Rest, []specs.ApiConnectorType{specs.JsonRpcConnector}, true},
		{"rest over grpc connector", protocol.Rest, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"grpc over grpc connector", protocol.Grpc, []specs.ApiConnectorType{specs.GrpcConnector}, true},
		{"grpc over json-rpc connector", protocol.Grpc, []specs.ApiConnectorType{specs.JsonRpcConnector}, false},
		{"grpc over rest connector", protocol.Grpc, []specs.ApiConnectorType{specs.RestConnector}, false},
		{"grpc when grpc is one of several connectors", protocol.Grpc, []specs.ApiConnectorType{specs.JsonRpcConnector, specs.GrpcConnector}, true},
		{"no connectors", protocol.JsonRpc, nil, false},
		{"grpc with no connectors", protocol.Grpc, nil, false},
		{"unknown request type", protocol.Unknown, []specs.ApiConnectorType{specs.JsonRpcConnector}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, test.requestType.CanBeServedBy(test.connectors))
		})
	}
}
