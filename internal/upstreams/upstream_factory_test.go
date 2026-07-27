package upstreams

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/aptos_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	"github.com/stretchr/testify/assert"
)

// stubConnector is a do-nothing ApiConnector for factory tests: the factory
// only stores the connector, so no methods are ever called. It cannot use
// pkg/test_utils/mocks because that package imports internal/upstreams, which
// would create an import cycle from an internal (white-box) test.
type stubConnector struct {
	connectors.ApiConnector
}

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
