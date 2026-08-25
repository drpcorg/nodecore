package blocks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

// stubConnector only reports its type; createHead never calls anything else
// (the test_utils mocks would import blocks - a cycle for an internal test).
type stubConnector struct {
	connectorType specs.ApiConnectorType
}

func (s *stubConnector) Start() {}

func (s *stubConnector) Stop() {}

func (s *stubConnector) Running() bool {
	return true
}

func (s *stubConnector) SendRequest(context.Context, protocol.RequestHolder) protocol.ResponseHolder {
	return nil
}

func (s *stubConnector) Subscribe(context.Context, protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}

func (s *stubConnector) Unsubscribe(string) {}

func (s *stubConnector) GetType() specs.ApiConnectorType {
	return s.connectorType
}

func (s *stubConnector) GetUrl() string {
	return ""
}

func (s *stubConnector) SubscribeStates(string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

// stubSpecific is a BlockChainSpecific whose SubscribeHeadRequest outcome is
// configurable; nothing else is exercised by createHead.
type stubSpecific struct {
	subErr error
}

func (s *stubSpecific) GetLatestBlock(context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) GetFinalizedBlock(context.Context) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) ParseBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) ParseSubscriptionBlock([]byte) (protocol.Block, error) {
	return protocol.ZeroBlock{}, nil
}

func (s *stubSpecific) SubscribeHeadRequest() (protocol.RequestHolder, error) {
	if s.subErr != nil {
		return nil, s.subErr
	}
	return protocol.NewInternalUpstreamGrpcRequest("/pkg.Service/Subscribe", nil, chains.SUI), nil
}

func TestCreateHead(t *testing.T) {
	options := &chains.Options{InternalTimeout: time.Second}
	tests := []struct {
		name          string
		connectorType specs.ApiConnectorType
		headMode      config.HeadMode
		subErr        error
		expected      Head
	}{
		{"json-rpc polls regardless of head-mode", specs.JsonRpcConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"rest polls", specs.RestConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"tendermint polls", specs.TendermintConnector, config.HeadModeSubscribe, nil, &RpcHead{}},
		{"websocket subscribes regardless of head-mode", specs.WebsocketConnector, config.HeadModePoll, nil, &SubscriptionHead{}},
		{"grpc poll mode", specs.GrpcConnector, config.HeadModePoll, nil, &RpcHead{}},
		{"grpc subscribe mode with support", specs.GrpcConnector, config.HeadModeSubscribe, nil, &SubscriptionHead{}},
		{"grpc subscribe mode without chain support falls back to polling", specs.GrpcConnector, config.HeadModeSubscribe, ErrUnsupportedHeadSubscriptions, &RpcHead{}},
		{"grpc subscribe mode with a real error stays a subscription head", specs.GrpcConnector, config.HeadModeSubscribe, errors.New("marshal failed"), &SubscriptionHead{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := &stubConnector{connectorType: tt.connectorType}
			head := createHead(t.Context(), "id", time.Second, tt.headMode, connector, &stubSpecific{subErr: tt.subErr}, options)
			assert.IsType(t, tt.expected, head)
		})
	}
}
