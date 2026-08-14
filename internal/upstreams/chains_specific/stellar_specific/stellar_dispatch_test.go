package stellar_specific_test

import (
	"context"
	"testing"
	"time"

	"github.com/drpcorg/nodecore/internal/upstreams/chains_specific/stellar_specific"
	"github.com/drpcorg/nodecore/internal/upstreams/connectors"
	"github.com/drpcorg/nodecore/pkg/chains"
	specs "github.com/drpcorg/nodecore/pkg/methods"
	"github.com/drpcorg/nodecore/pkg/test_utils/mocks"
	"github.com/stretchr/testify/assert"
)

func newStellarSpecific(primary connectors.ApiConnector) any {
	return stellar_specific.NewStellarChainSpecificObject(
		context.Background(),
		chains.GetChain("stellar"),
		"id",
		primary,
		time.Second,
		&chains.Options{
			InternalTimeout:        5 * time.Second,
			ValidationInterval:     10 * time.Second,
			DisableChainValidation: new(false),
			ValidateSyncing:        new(false),
		},
	)
}

func TestStellarDispatchPicksHorizonForARestConnector(t *testing.T) {
	rest := mocks.NewConnectorMockWithType(specs.RestConnector)

	specific := newStellarSpecific(rest)

	assert.IsType(t, &stellar_specific.StellarHorizonChainSpecificObject{}, specific)
}

func TestStellarDispatchPicksRpcForAJsonRpcConnector(t *testing.T) {
	jsonRpc := mocks.NewConnectorMockWithType(specs.JsonRpcConnector)

	specific := newStellarSpecific(jsonRpc)

	assert.IsType(t, &stellar_specific.StellarRpcChainSpecificObject{}, specific)
}

// A nil primary connector cannot happen through the factory, but the dispatcher
// must not panic on it either - it falls back to the stellar-rpc flavor.
func TestStellarDispatchFallsBackToRpcForANilConnector(t *testing.T) {
	specific := newStellarSpecific(nil)

	assert.IsType(t, &stellar_specific.StellarRpcChainSpecificObject{}, specific)
}
