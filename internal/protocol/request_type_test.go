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
		{"json-rpc over grpc connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"json-rpc over rest connector", protocol.JsonRpc, []specs.ApiConnectorType{specs.RestConnector}, false},
		{"json-rpc when any connector matches", protocol.JsonRpc, []specs.ApiConnectorType{specs.GrpcConnector, specs.JsonRpcConnector}, true},
		{"ws over websocket connector", protocol.Ws, []specs.ApiConnectorType{specs.WebsocketConnector}, true},
		{"ws over json-rpc connector", protocol.Ws, []specs.ApiConnectorType{specs.JsonRpcConnector}, true},
		{"ws over grpc connector", protocol.Ws, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"rest over rest connector", protocol.Rest, []specs.ApiConnectorType{specs.RestConnector}, true},
		{"rest over rest-indexer connector", protocol.Rest, []specs.ApiConnectorType{specs.RestIndexer}, true},
		{"rest over rest-additional connector", protocol.Rest, []specs.ApiConnectorType{specs.RestAdditional}, true},
		{"rest over tendermint connector", protocol.Rest, []specs.ApiConnectorType{specs.TendermintConnector}, true},
		{"rest over json-rpc connector", protocol.Rest, []specs.ApiConnectorType{specs.JsonRpcConnector}, false},
		{"rest over grpc connector", protocol.Rest, []specs.ApiConnectorType{specs.GrpcConnector}, false},
		{"grpc over grpc connector", protocol.Grpc, []specs.ApiConnectorType{specs.GrpcConnector}, true},
		{"grpc over json-rpc connector", protocol.Grpc, []specs.ApiConnectorType{specs.JsonRpcConnector}, false},
		{"grpc over websocket connector", protocol.Grpc, []specs.ApiConnectorType{specs.WebsocketConnector}, false},
		{"unknown connector type", protocol.JsonRpc, []specs.ApiConnectorType{specs.UnknownType}, false},
		{"no connectors", protocol.JsonRpc, nil, false},
		{"unknown request type", protocol.Unknown, []specs.ApiConnectorType{specs.JsonRpcConnector}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, test.requestType.CanBeServedBy(test.connectors))
		})
	}
}
