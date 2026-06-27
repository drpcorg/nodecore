package upstreams

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aptos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/stretchr/testify/assert"
)

// stubConnector is a minimal ApiConnector for use in factory tests.
// It cannot use pkg/test_utils/mocks because that package imports internal/upstreams,
// which would create an import cycle when used from an internal (white-box) test.
type stubConnector struct{}

func (s *stubConnector) Start()           {}
func (s *stubConnector) Stop()            {}
func (s *stubConnector) Running() bool    { return false }
func (s *stubConnector) GetUrl() string   { return "" }
func (s *stubConnector) GetType() specs.ApiConnectorType { return specs.JsonRpcConnector }
func (s *stubConnector) SendRequest(_ context.Context, _ protocol.RequestHolder) protocol.ResponseHolder {
	return nil
}
func (s *stubConnector) Subscribe(_ context.Context, _ protocol.RequestHolder) (protocol.UpstreamSubscriptionResponse, error) {
	return nil, nil
}
func (s *stubConnector) Unsubscribe(_ string) {}
func (s *stubConnector) SubscribeStates(name string) *utils.Subscription[protocol.SubscribeConnectorState] {
	return nil
}

var _ connectors.ApiConnector = (*stubConnector)(nil)

func TestGetChainSpecificReturnsAptos(t *testing.T) {
	ctx := context.Background()
	conf := &config.Upstream{Id: "u1", Options: newAptosTestOptions()}
	info := &connectorsInfo{internalRequestConnector: &stubConnector{}}
	cs, err := getChainSpecific(ctx, conf, info, chains.GetChain("aptos-mainnet"))
	assert.NoError(t, err)
	assert.IsType(t, &aptos_specific.AptosChainSpecificObject{}, cs)
}

func newAptosTestOptions() *chains.Options {
	disabled := false
	return &chains.Options{
		InternalTimeout:         time.Second,
		ValidationInterval:      time.Second,
		DisableChainValidation:  &disabled,
		DisableHealthValidation: &disabled,
	}
}
